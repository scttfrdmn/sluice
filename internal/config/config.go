// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package config loads config/sluice.yaml (and an optional gitignored
// config/local.yaml for account-specific values). Model IDs, prices,
// taxonomy, thresholds and seeds live here and nowhere in code
// (PROJECT.md §6, §7).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the whole of config/sluice.yaml.
type Config struct {
	// Backend selects the model client: "bedrock" for real runs, "fake" for
	// the testdata pipeline.
	Backend string `yaml:"backend"`
	Region  string `yaml:"region"`

	Hosts []Host `yaml:"hosts"`

	Models      []Model `yaml:"models"`
	LocalModels []Model `yaml:"local_models"`
	// Strong is the model name used as tagger and reference generator.
	Strong string `yaml:"strong"`
	Prices Prices `yaml:"prices"`

	Judges   Judges   `yaml:"judges"`
	Taxonomy Taxonomy `yaml:"taxonomy"`
	Matrix   Matrix   `yaml:"matrix"`
	Label    Label    `yaml:"label"`
	Harvest  Harvest  `yaml:"harvest"`
	Report   Report   `yaml:"report"`
	Seeds    Seeds    `yaml:"seeds"`

	// Prompts are template files, relative to the config file's directory.
	Prompts Prompts `yaml:"prompts"`

	// Batch is account-specific and comes from config/local.yaml or the
	// environment, never from the committed config.
	Batch Batch `yaml:"batch"`

	dir string
}

// Host is one machine whose Claude Code sessions are pulled.
type Host struct {
	Name  string `yaml:"name"`
	Local bool   `yaml:"local"`
}

// Model is one entry in the pool.
type Model struct {
	Name     string `yaml:"name"`     // short stable name used in the matrix
	ID       string `yaml:"id"`       // backend model / inference-profile ID
	Provider string `yaml:"provider"` // anthropic, amazon, ...
	Tier     string `yaml:"tier"`     // S, M, L
	// Prices in USD per million tokens. Batch prices are optional; when
	// absent a batch cell is priced at on-demand rates and flagged.
	InPerM       float64  `yaml:"in_per_m"`
	OutPerM      float64  `yaml:"out_per_m"`
	BatchInPerM  *float64 `yaml:"batch_in_per_m"`
	BatchOutPerM *float64 `yaml:"batch_out_per_m"`
	Concurrency  int      `yaml:"concurrency"`
	RPM          int      `yaml:"rpm"`
	// Endpoint is used by the local (OpenAI-compatible) lane only.
	Endpoint string `yaml:"endpoint"`
}

// Prices records where the price table came from.
type Prices struct {
	Date   string `yaml:"date"`
	Source string `yaml:"source"`
	Region string `yaml:"region"`
	Note   string `yaml:"note"`
}

// Judges configures the rung-3 judge pair.
type Judges struct {
	// TierPreference orders candidate judges within each provider; the
	// first that is not the judged model is used.
	TierPreference []string `yaml:"tier_preference"`
	// Providers lists which providers contribute one judge each.
	Providers []string `yaml:"providers"`
	MaxTokens int      `yaml:"max_tokens"`
}

// Taxonomy is the class space. Changing it is a re-tag, never a matrix rerun.
type Taxonomy struct {
	Version       string   `yaml:"version"`
	Operations    []string `yaml:"operations"`
	Domains       []string `yaml:"domains"`
	VerifierHints []string `yaml:"verifier_hints"`
	MaxLeaves     int      `yaml:"max_leaves"`
	TagMaxTokens  int      `yaml:"tag_max_tokens"`
	ContextTokens int      `yaml:"context_tokens"`
	CharsPerToken float64  `yaml:"chars_per_token"`
}

// Matrix configures cmd/matrix.
type Matrix struct {
	Draws             int     `yaml:"draws"`
	MaxTokens         int     `yaml:"max_tokens"`
	BatchThreshold    int     `yaml:"batch_threshold"`
	BatchMinRecords   int     `yaml:"batch_min_records"`
	BatchMaxRecords   int     `yaml:"batch_max_records"`
	OnDemandSubsample float64 `yaml:"on_demand_subsample"`
	FlushRows         int     `yaml:"flush_rows"`
	FlushSeconds      int     `yaml:"flush_seconds"`
	MaxAttempts       int     `yaml:"max_attempts"`
}

// Label configures cmd/label.
type Label struct {
	Sandbox        string            `yaml:"sandbox"` // docker | fake
	TimeoutSeconds int               `yaml:"timeout_seconds"`
	Images         map[string]string `yaml:"images"`
	Concurrency    int               `yaml:"concurrency"` // sandbox runs in parallel
	Workers        int               `yaml:"workers"`     // cells labeled in parallel
	RefMaxTokens   int               `yaml:"ref_max_tokens"`
}

// Harvest configures cmd/harvest.
type Harvest struct {
	MinChars       int      `yaml:"min_chars"`
	DedupJaccard   float64  `yaml:"dedup_jaccard"`
	DropSampleSize int      `yaml:"drop_sample_size"`
	Owner          string   `yaml:"owner"`
	ExcludeRepos   []string `yaml:"exclude_repos"`
	// PromptFileOwners are the GitHub owners whose repos contribute prompt
	// files; repos with no remote also contribute. Others are excluded and
	// listed in repo_prompt_files.txt.
	PromptFileOwners []string `yaml:"prompt_file_owners"`
	// WildchatOversample is the factor by which the Python helper
	// oversamples, so that N records survive the Go-side filters.
	WildchatOversample float64 `yaml:"wildchat_oversample"`
	WildchatDataset    string  `yaml:"wildchat_dataset"`
	WildchatFallback   string  `yaml:"wildchat_fallback"`
	// E0PerStratum and E0Split define the E0 subsample: this many requests
	// per stratum (source; repos split into issues and prompt files) from
	// this split. The full corpus is for E1 tagger training and E2.
	E0PerStratum int    `yaml:"e0_per_stratum"`
	E0Split      string `yaml:"e0_split"`
}

// Report configures cmd/report.
type Report struct {
	Delta      float64   `yaml:"delta"`
	Thresholds []float64 `yaml:"thresholds"`
	Bootstrap  int       `yaml:"bootstrap"`
	RhoProceed float64   `yaml:"rho_proceed"`
}

// Seeds are every random seed the pipeline uses.
type Seeds struct {
	Split     uint64 `yaml:"split"`
	Subsample uint64 `yaml:"subsample"`
	Wildchat  uint64 `yaml:"wildchat"`
	Bootstrap uint64 `yaml:"bootstrap"`
	Minhash   uint64 `yaml:"minhash"`
	E0Sample  uint64 `yaml:"e0_sample"`
	Pilot     uint64 `yaml:"pilot"`
}

// Prompts names the prompt template files.
type Prompts struct {
	Tag     string `yaml:"tag"`
	Reclass string `yaml:"reclass"`
	Judge   string `yaml:"judge"`
	Test    string `yaml:"test"`
	Numeric string `yaml:"numeric"`
}

// Batch holds the batch-inference S3 location and IAM role. These are
// account identifiers and are never committed.
type Batch struct {
	S3URI   string `yaml:"s3_uri"`
	RoleARN string `yaml:"role_arn"`
}

// Load reads path and, if present, local.yaml beside it. Environment
// variables SLUICE_BATCH_S3_URI and SLUICE_BATCH_ROLE_ARN override both.
func Load(path string) (*Config, error) {
	c := &Config{}
	if err := readYAML(path, c); err != nil {
		return nil, err
	}
	c.dir = filepath.Dir(path)
	local := filepath.Join(c.dir, "local.yaml")
	if _, err := os.Stat(local); err == nil {
		var l struct {
			Batch Batch `yaml:"batch"`
		}
		if err := readYAML(local, &l); err != nil {
			return nil, err
		}
		if l.Batch.S3URI != "" {
			c.Batch.S3URI = l.Batch.S3URI
		}
		if l.Batch.RoleARN != "" {
			c.Batch.RoleARN = l.Batch.RoleARN
		}
	}
	if v := os.Getenv("SLUICE_BATCH_S3_URI"); v != "" {
		c.Batch.S3URI = v
	}
	if v := os.Getenv("SLUICE_BATCH_ROLE_ARN"); v != "" {
		c.Batch.RoleARN = v
	}
	return c, c.validate()
}

func readYAML(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (c *Config) validate() error {
	var errs []error
	if c.Backend != "bedrock" && c.Backend != "fake" {
		errs = append(errs, fmt.Errorf("backend must be bedrock or fake, got %q", c.Backend))
	}
	if c.Prices.Date == "" {
		errs = append(errs, errors.New("prices.date is required"))
	}
	seen := map[string]bool{}
	for _, m := range c.Models {
		if m.Name == "" || m.ID == "" || m.Provider == "" || m.Tier == "" {
			errs = append(errs, fmt.Errorf("model %+v: name, id, provider, tier are required", m))
		}
		if seen[m.Name] {
			errs = append(errs, fmt.Errorf("duplicate model name %q", m.Name))
		}
		seen[m.Name] = true
	}
	if _, ok := c.Model(c.Strong); !ok {
		errs = append(errs, fmt.Errorf("strong model %q is not in the pool", c.Strong))
	}
	if c.Matrix.Draws != 2 {
		// PROJECT.md §7: two draws, not more, unless recorded on an issue.
		errs = append(errs, fmt.Errorf("matrix.draws = %d; PROJECT.md §7 fixes it at 2", c.Matrix.Draws))
	}
	if len(c.Taxonomy.Operations) == 0 || len(c.Taxonomy.Domains) == 0 {
		errs = append(errs, errors.New("taxonomy operations and domains are required"))
	}
	return errors.Join(errs...)
}

// Path resolves a path relative to the config directory.
func (c *Config) Path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

// Model returns the pool entry named name.
func (c *Config) Model(name string) (Model, bool) {
	for _, m := range c.Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// JudgesFor returns the judge pair for a cell produced by judged: one model
// per configured provider, never the judged model itself, chosen by tier
// preference (PROJECT.md §6).
func (c *Config) JudgesFor(judged string) ([]Model, error) {
	var out []Model
	for _, p := range c.Judges.Providers {
		var pick *Model
		for _, tier := range c.Judges.TierPreference {
			for i, m := range c.Models {
				if m.Provider == p && m.Tier == tier && m.Name != judged {
					pick = &c.Models[i]
					break
				}
			}
			if pick != nil {
				break
			}
		}
		if pick == nil {
			return nil, fmt.Errorf("no judge available from provider %s for %s", p, judged)
		}
		out = append(out, *pick)
	}
	return out, nil
}

// ValidOperation reports whether op is in the taxonomy.
func (t Taxonomy) ValidOperation(op string) bool { return slices.Contains(t.Operations, op) }

// ValidDomain reports whether d is in the taxonomy.
func (t Taxonomy) ValidDomain(d string) bool { return slices.Contains(t.Domains, d) }

// ValidHint reports whether h is an allowed verifier hint. Hints are either
// exact entries or "prefix:*" entries.
func (t Taxonomy) ValidHint(h string) bool {
	for _, v := range t.VerifierHints {
		if v == h {
			return true
		}
		if pre, ok := strings.CutSuffix(v, "*"); ok && strings.HasPrefix(h, pre) && len(h) > len(pre) {
			return true
		}
	}
	return false
}

// Cost prices a call from the configured table. For batch calls without a
// batch price, on-demand rates are used and the basis says so.
func (m Model) Cost(tokensIn, tokensOut int, batch bool) (usd float64, basis string) {
	in, out, basis := m.InPerM, m.OutPerM, "on-demand"
	if batch {
		if m.BatchInPerM != nil && m.BatchOutPerM != nil {
			in, out, basis = *m.BatchInPerM, *m.BatchOutPerM, "batch"
		} else {
			basis = "on-demand-fallback"
		}
	}
	return (float64(tokensIn)*in + float64(tokensOut)*out) / 1e6, basis
}
