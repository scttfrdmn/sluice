// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package matrix

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/jsonl"
	"github.com/scttfrdmn/sluice/internal/llm"
	"github.com/scttfrdmn/sluice/internal/spend"
)

// Backend is a client that can run calls on demand and in batch.
type Backend interface {
	llm.Client
	llm.BatchClient
}

// Options for one matrix pass.
type Options struct {
	Dir       string // matrix part-file directory
	ErrorsLog string // matrix_errors.jsonl
	Log       io.Writer
	// MaxSpend is the stage cap in USD, cumulative with the cost of cells
	// already in the matrix (package spend).
	MaxSpend float64
	// OnDemand runs every cell on demand, never in batch (the pilot, or no
	// batch configuration).
	OnDemand bool
}

// CellError records a cell that failed after all retries. It is retried on
// the next run.
type CellError struct {
	Key   string `json:"key"`
	Mode  string `json:"mode"`
	Error string `json:"error"`
	At    string `json:"at"`
}

type job struct {
	leaf      corpus.Leaf
	model     config.Model
	draw      int
	subsample bool
}

// InSubsample reports whether (leaf, model) is in the seeded on-demand
// latency subsample. Both draws of a selected pair go on demand.
func InSubsample(seed uint64, leaf, model string, frac float64) bool {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], seed)
	h := sha256.Sum256(append(append(b[:], leaf...), "|"+model...))
	return float64(binary.LittleEndian.Uint64(h[:8])>>11)/float64(1<<53) < frac
}

// Run fills every missing cell for leaves × pool models × draws.
func Run(ctx context.Context, cfg *config.Config, be Backend, leaves []corpus.Leaf, o Options) error {
	existing, err := ReadAll(o.Dir)
	if err != nil {
		return err
	}
	inScope := make(map[string]bool, len(leaves))
	for _, l := range leaves {
		inScope[l.LeafID] = true
	}
	have := make(map[string]bool, len(existing))
	var prior float64 // cost of existing cells for these leaves counts against the cap
	for _, c := range existing {
		have[c.Key()] = true
		if inScope[c.LeafID] {
			prior += c.CostUSD
		}
	}
	budget := spend.New(o.MaxSpend, prior)
	var pending []job
	for _, l := range leaves {
		for _, m := range cfg.Models {
			sub := InSubsample(cfg.Seeds.Subsample, l.LeafID, m.Name, cfg.Matrix.OnDemandSubsample)
			for d := 0; d < cfg.Matrix.Draws; d++ {
				if !have[CellKey(l.LeafID, m.Name, d)] {
					pending = append(pending, job{leaf: l, model: m, draw: d, subsample: sub})
				}
			}
		}
	}
	fmt.Fprintf(o.Log, "matrix: %d cells exist, %d pending (%d leaves × %d models × %d draws)\n",
		len(existing), len(pending), len(leaves), len(cfg.Models), cfg.Matrix.Draws)
	if len(pending) == 0 {
		fmt.Fprintln(o.Log, budget.Summary("matrix"))
		return nil
	}

	// Mode: batch above the threshold, except the latency subsample and any
	// per-model remainder too small for a batch job.
	useBatch := !o.OnDemand && len(pending) > cfg.Matrix.BatchThreshold
	onDemand := []job{}
	batchByModel := map[string][]job{}
	for _, j := range pending {
		if useBatch && !j.subsample {
			batchByModel[j.model.Name] = append(batchByModel[j.model.Name], j)
		} else {
			onDemand = append(onDemand, j)
		}
	}
	for name, js := range batchByModel {
		if len(js) < cfg.Matrix.BatchMinRecords {
			onDemand = append(onDemand, js...)
			delete(batchByModel, name)
		}
	}
	var nBatch int
	for _, js := range batchByModel {
		nBatch += len(js)
	}
	fmt.Fprintf(o.Log, "matrix: %d on-demand, %d batch\n", len(onDemand), nBatch)

	w, err := NewWriter(o.Dir, cfg.Matrix.FlushRows, time.Duration(max(1, cfg.Matrix.FlushSeconds))*time.Second)
	if err != nil {
		return err
	}
	errs, err := jsonl.OpenAppender(o.ErrorsLog)
	if err != nil {
		return err
	}
	defer errs.Close()

	var nFail atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return runOnDemand(gctx, cfg, be, onDemand, w, errs, &nFail, budget, o.Log) })
	for _, js := range batchByModel {
		g.Go(func() error { return runBatch(gctx, cfg, be, js, w, errs, &nFail, budget, o.Log) })
	}
	runErr := g.Wait()
	n, werr := w.Close()
	fmt.Fprintf(o.Log, "matrix: wrote %d cells, %d failed (logged to %s)\n", n, nFail.Load(), o.ErrorsLog)
	fmt.Fprintln(o.Log, budget.Summary("matrix"))
	if runErr != nil {
		return runErr
	}
	if werr != nil {
		return werr
	}
	if nFail.Load() > 0 {
		return fmt.Errorf("%d cells failed; rerun to retry them", nFail.Load())
	}
	if budget.Stopped() {
		return fmt.Errorf("matrix: %w; %d of %d pending cells written", spend.ErrCap, n, len(pending))
	}
	return nil
}

// worstCase is the most a batch record can cost: the prompt (chars per
// token with a 1.5× margin) plus max_tokens of output, at the price the
// cell will be recorded at.
func worstCase(cfg *config.Config, j job) float64 {
	in := int(1.5*float64(len(j.leaf.Prompt()))/cfg.Taxonomy.CharsPerToken) + 50
	c, _ := j.model.Cost(in, cfg.Matrix.MaxTokens, true)
	return c
}

func request(cfg *config.Config, j job) llm.Request {
	return llm.Request{
		Purpose:   llm.PurposeAnswer,
		Prompt:    j.leaf.Prompt(),
		MaxTokens: cfg.Matrix.MaxTokens,
		Draw:      j.draw,
		Meta:      map[string]string{"class": j.leaf.Class, "leaf_id": j.leaf.LeafID, "hint": j.leaf.VerifierHint},
	}
}

func baseCell(cfg *config.Config, j job, r llm.Response, mode string) Cell {
	cost, basis := j.model.Cost(r.TokensIn, r.TokensOut, mode == ModeBatch)
	return Cell{
		LeafID: j.leaf.LeafID, RequestID: j.leaf.RequestID, Class: j.leaf.Class,
		Model: j.model.Name, ModelID: j.model.ID, Draw: int32(j.draw),
		Output: r.Text, TokensIn: int64(r.TokensIn), TokensOut: int64(r.TokensOut),
		Mode: mode, Subsample: j.subsample, CostUSD: cost, PriceBasis: basis, PriceDate: cfg.Prices.Date,
		StopReason: r.StopReason, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func runOnDemand(ctx context.Context, cfg *config.Config, be Backend, jobs []job, w *Writer, errs *jsonl.Appender, nFail *atomic.Int64, budget *spend.Budget, log io.Writer) error {
	type lane struct {
		sem chan struct{}
		lim *rate.Limiter
	}
	lanes := map[string]*lane{}
	for _, m := range cfg.Models {
		c := max(1, m.Concurrency)
		lim := rate.NewLimiter(rate.Inf, c)
		if m.RPM > 0 {
			lim = rate.NewLimiter(rate.Limit(float64(m.RPM)/60), c)
		}
		lanes[m.Name] = &lane{sem: make(chan struct{}, c), lim: lim}
	}
	var done atomic.Int64
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	for _, j := range jobs {
		if budget.Exhausted() {
			break // in-flight calls finish; the rest stay pending
		}
		l := lanes[j.model.Name]
		select {
		case l.sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-l.sem }()
			if err := l.lim.Wait(ctx); err != nil {
				return
			}
			r, err := be.Invoke(ctx, j.model, request(cfg, j))
			if err != nil {
				nFail.Add(1)
				errs.Append(CellError{Key: CellKey(j.leaf.LeafID, j.model.Name, j.draw), Mode: ModeOnDemand, Error: err.Error(), At: time.Now().UTC().Format(time.RFC3339)})
				return
			}
			c := baseCell(cfg, j, r, ModeOnDemand)
			budget.Add(c.CostUSD)
			lat := r.LatencyMs
			c.LatencyMs = &lat
			if err := w.Add(c); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
			if n := done.Add(1); n%500 == 0 {
				fmt.Fprintf(log, "matrix: %d/%d on-demand cells\n", n, len(jobs))
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func runBatch(ctx context.Context, cfg *config.Config, be Backend, jobs []job, w *Writer, errs *jsonl.Appender, nFail *atomic.Int64, budget *spend.Budget, log io.Writer) error {
	m := jobs[0].model
	maxRec := max(cfg.Matrix.BatchMinRecords, cfg.Matrix.BatchMaxRecords)
	for start := 0; start < len(jobs); {
		chunk := jobs[start:min(len(jobs), start+maxRec)]
		wc := make([]float64, len(chunk))
		for i, j := range chunk {
			wc[i] = worstCase(cfg, j)
		}
		k, reserved := budget.Reserve(wc, cfg.Matrix.BatchMinRecords)
		if k == 0 {
			fmt.Fprintf(log, "matrix: batch for %s deferred at the spend cap: %d records left pending\n", m.Name, len(jobs)-start)
			return nil
		}
		chunk = chunk[:k]
		items := make([]llm.BatchItem, len(chunk))
		byID := make(map[string]job, len(chunk))
		for i, j := range chunk {
			id := fmt.Sprintf("r%08d", start+i)
			items[i] = llm.BatchItem{RecordID: id, Request: request(cfg, j)}
			byID[id] = j
		}
		fmt.Fprintf(log, "matrix: batch job for %s, %d records (worst-case reservation $%.2f)\n", m.Name, len(items), reserved)
		res, wall, err := be.RunBatch(ctx, m, items)
		var actual float64
		if err != nil {
			budget.Settle(reserved, 0)
			// The whole job failed: log every cell so a rerun retries them.
			for _, j := range byID {
				nFail.Add(1)
				errs.Append(CellError{Key: CellKey(j.leaf.LeafID, j.model.Name, j.draw), Mode: ModeBatch, Error: err.Error(), At: time.Now().UTC().Format(time.RFC3339)})
			}
			fmt.Fprintf(log, "matrix: batch job for %s failed: %v\n", m.Name, err)
			start += len(chunk)
			continue
		}
		for id, j := range byID {
			br, ok := res[id]
			if !ok || br.Err != "" {
				nFail.Add(1)
				msg := br.Err
				if !ok {
					msg = "record missing from batch output"
				}
				errs.Append(CellError{Key: CellKey(j.leaf.LeafID, j.model.Name, j.draw), Mode: ModeBatch, Error: msg, At: time.Now().UTC().Format(time.RFC3339)})
				continue
			}
			c := baseCell(cfg, j, br.Response, ModeBatch)
			actual += c.CostUSD
			wl := wall
			c.BatchWallMs = &wl
			if err := w.Add(c); err != nil {
				budget.Settle(reserved, actual)
				return err
			}
		}
		budget.Settle(reserved, actual)
		start += len(chunk)
	}
	return nil
}
