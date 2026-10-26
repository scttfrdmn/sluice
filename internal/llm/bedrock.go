// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	rtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/scttfrdmn/sluice/internal/config"
)

// Bedrock calls models through the Bedrock Converse API (on-demand) and
// CreateModelInvocationJob (batch).
type Bedrock struct {
	rt    *bedrockruntime.Client
	ctl   *bedrock.Client
	s3    *s3.Client
	batch config.Batch
	poll  time.Duration
}

// NewBedrock builds a client from the default AWS credential chain.
func NewBedrock(ctx context.Context, cfg *config.Config) (*Bedrock, error) {
	attempts := cfg.Matrix.MaxAttempts
	if attempts <= 0 {
		attempts = 6
	}
	ac, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithRetryer(func() aws.Retryer {
			return retry.NewAdaptiveMode(func(o *retry.AdaptiveModeOptions) {
				o.StandardOptions = append(o.StandardOptions, func(so *retry.StandardOptions) {
					so.MaxAttempts = attempts
					so.MaxBackoff = 60 * time.Second
				})
			})
		}))
	if err != nil {
		return nil, err
	}
	return &Bedrock{
		rt:    bedrockruntime.NewFromConfig(ac),
		ctl:   bedrock.NewFromConfig(ac),
		s3:    s3.NewFromConfig(ac),
		batch: cfg.Batch,
		poll:  60 * time.Second,
	}, nil
}

// Invoke makes one Converse call. Latency covers the whole call including
// SDK retries on throttling, i.e. the queueing a caller actually sees.
func (b *Bedrock) Invoke(ctx context.Context, m config.Model, req Request) (Response, error) {
	in := &bedrockruntime.ConverseInput{
		ModelId: aws.String(m.ID),
		Messages: []rtypes.Message{{
			Role:    rtypes.ConversationRoleUser,
			Content: []rtypes.ContentBlock{&rtypes.ContentBlockMemberText{Value: req.Prompt}},
		}},
	}
	if req.MaxTokens > 0 {
		in.InferenceConfig = &rtypes.InferenceConfiguration{MaxTokens: aws.Int32(int32(req.MaxTokens))}
	}
	start := time.Now()
	out, err := b.rt.Converse(ctx, in)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return Response{LatencyMs: lat}, err
	}
	var sb strings.Builder
	if msg, ok := out.Output.(*rtypes.ConverseOutputMemberMessage); ok {
		for _, c := range msg.Value.Content {
			if t, ok := c.(*rtypes.ContentBlockMemberText); ok {
				sb.WriteString(t.Value)
			}
		}
	}
	r := Response{Text: sb.String(), LatencyMs: lat, StopReason: string(out.StopReason)}
	if out.Usage != nil {
		r.TokensIn = int(aws.ToInt32(out.Usage.InputTokens))
		r.TokensOut = int(aws.ToInt32(out.Usage.OutputTokens))
	}
	return r, nil
}

// nativeBody is the InvokeModel request body for batch jobs, which take the
// model-native schema rather than Converse.
func nativeBody(m config.Model, req Request) (json.RawMessage, error) {
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}
	var body any
	switch m.Provider {
	case "anthropic":
		body = map[string]any{
			"anthropic_version": "bedrock-2023-05-31",
			"max_tokens":        maxTok,
			"messages": []any{map[string]any{
				"role":    "user",
				"content": []any{map[string]any{"type": "text", "text": req.Prompt}},
			}},
		}
	case "amazon":
		body = map[string]any{
			"schemaVersion":   "messages-v1",
			"messages":        []any{map[string]any{"role": "user", "content": []any{map[string]any{"text": req.Prompt}}}},
			"inferenceConfig": map[string]any{"maxTokens": maxTok},
		}
	default:
		return nil, fmt.Errorf("no batch body format for provider %q", m.Provider)
	}
	return json.Marshal(body)
}

func parseNative(provider string, raw json.RawMessage) (Response, error) {
	var r Response
	switch provider {
	case "anthropic":
		var v struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StopReason string `json:"stop_reason"`
			Usage      struct {
				In  int `json:"input_tokens"`
				Out int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return r, err
		}
		for _, c := range v.Content {
			if c.Type == "text" {
				r.Text += c.Text
			}
		}
		r.TokensIn, r.TokensOut, r.StopReason = v.Usage.In, v.Usage.Out, v.StopReason
	case "amazon":
		var v struct {
			Output struct {
				Message struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			} `json:"output"`
			StopReason string `json:"stopReason"`
			Usage      struct {
				In  int `json:"inputTokens"`
				Out int `json:"outputTokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return r, err
		}
		for _, c := range v.Output.Message.Content {
			r.Text += c.Text
		}
		r.TokensIn, r.TokensOut, r.StopReason = v.Usage.In, v.Usage.Out, v.StopReason
	default:
		return r, fmt.Errorf("no batch output format for provider %q", provider)
	}
	return r, nil
}

// RunBatch uploads items as one JSONL input, runs a model invocation job,
// waits for it, and reads the per-record outputs. The returned wall time is
// job submission to completion and is never a per-call latency.
func (b *Bedrock) RunBatch(ctx context.Context, m config.Model, items []BatchItem) (map[string]BatchResult, int64, error) {
	if b.batch.S3URI == "" || b.batch.RoleARN == "" {
		return nil, 0, errors.New("batch inference needs SLUICE_BATCH_S3_URI and SLUICE_BATCH_ROLE_ARN (or config/local.yaml)")
	}
	u, err := url.Parse(b.batch.S3URI)
	if err != nil || u.Scheme != "s3" {
		return nil, 0, fmt.Errorf("batch s3_uri must be s3://bucket/prefix")
	}
	bucket := u.Host
	jobName := fmt.Sprintf("sluice-%s-%d", strings.NewReplacer(".", "-", "_", "-").Replace(m.Name), time.Now().Unix())
	prefix := strings.Trim(path.Join(strings.TrimPrefix(u.Path, "/"), jobName), "/")
	inKey := prefix + "/input.jsonl"

	var buf bytes.Buffer
	for _, it := range items {
		body, err := nativeBody(m, it.Request)
		if err != nil {
			return nil, 0, err
		}
		line, _ := json.Marshal(map[string]any{"recordId": it.RecordID, "modelInput": body})
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if _, err := b.s3.PutObject(ctx, &s3.PutObjectInput{Bucket: &bucket, Key: &inKey, Body: bytes.NewReader(buf.Bytes())}); err != nil {
		return nil, 0, fmt.Errorf("upload batch input: %w", err)
	}

	start := time.Now()
	job, err := b.ctl.CreateModelInvocationJob(ctx, &bedrock.CreateModelInvocationJobInput{
		JobName: aws.String(jobName),
		ModelId: aws.String(m.ID),
		RoleArn: aws.String(b.batch.RoleARN),
		InputDataConfig: &btypes.ModelInvocationJobInputDataConfigMemberS3InputDataConfig{Value: btypes.ModelInvocationJobS3InputDataConfig{
			S3Uri: aws.String("s3://" + bucket + "/" + inKey),
		}},
		OutputDataConfig: &btypes.ModelInvocationJobOutputDataConfigMemberS3OutputDataConfig{Value: btypes.ModelInvocationJobS3OutputDataConfig{
			S3Uri: aws.String("s3://" + bucket + "/" + prefix + "/output/"),
		}},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("create batch job: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(b.poll):
		}
		st, err := b.ctl.GetModelInvocationJob(ctx, &bedrock.GetModelInvocationJobInput{JobIdentifier: job.JobArn})
		if err != nil {
			return nil, 0, err
		}
		switch st.Status {
		case btypes.ModelInvocationJobStatusCompleted, btypes.ModelInvocationJobStatusPartiallyCompleted:
			wall := time.Since(start).Milliseconds()
			res, err := b.readBatchOutput(ctx, m.Provider, bucket, prefix+"/output/")
			return res, wall, err
		case btypes.ModelInvocationJobStatusFailed, btypes.ModelInvocationJobStatusStopped, btypes.ModelInvocationJobStatusExpired:
			return nil, 0, fmt.Errorf("batch job %s ended %s: %s", jobName, st.Status, aws.ToString(st.Message))
		}
	}
}

func (b *Bedrock) readBatchOutput(ctx context.Context, provider, bucket, prefix string) (map[string]BatchResult, error) {
	out := map[string]BatchResult{}
	p := s3.NewListObjectsV2Paginator(b.s3, &s3.ListObjectsV2Input{Bucket: &bucket, Prefix: &prefix})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			if !strings.HasSuffix(aws.ToString(obj.Key), ".jsonl.out") {
				continue
			}
			got, err := b.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: obj.Key})
			if err != nil {
				return nil, err
			}
			err = scanBatchLines(got.Body, provider, out)
			got.Body.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func scanBatchLines(r io.Reader, provider string, out map[string]BatchResult) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var rec struct {
			RecordID    string          `json:"recordId"`
			ModelOutput json.RawMessage `json:"modelOutput"`
			Error       *struct {
				Message string `json:"errorMessage"`
			} `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return err
		}
		if rec.Error != nil || len(rec.ModelOutput) == 0 {
			msg := "no model output"
			if rec.Error != nil {
				msg = rec.Error.Message
			}
			out[rec.RecordID] = BatchResult{Err: msg}
			continue
		}
		resp, err := parseNative(provider, rec.ModelOutput)
		if err != nil {
			out[rec.RecordID] = BatchResult{Err: err.Error()}
			continue
		}
		out[rec.RecordID] = BatchResult{Response: resp}
	}
	return sc.Err()
}
