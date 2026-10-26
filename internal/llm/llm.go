// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package llm is the model-call boundary. Every stage calls models through
// Client; the backend (Bedrock, fake, local OpenAI-compatible) is chosen by
// config, and model IDs come only from config.
package llm

import (
	"context"
	"fmt"

	"github.com/scttfrdmn/sluice/internal/config"
)

// Purpose says what a call is for. Real backends ignore it; the fake
// backend uses it to produce structurally valid responses.
type Purpose string

const (
	PurposeAnswer  Purpose = "answer"
	PurposeTag     Purpose = "tag"
	PurposeReclass Purpose = "reclass"
	PurposeJudge   Purpose = "judge"
	PurposeTest    Purpose = "test"
	PurposeNumeric Purpose = "numeric"
)

// Request is one single-turn call.
type Request struct {
	Purpose   Purpose
	Prompt    string
	MaxTokens int
	// Draw distinguishes repeated identical calls. Real backends ignore it
	// (temperature is the model default); the fake uses it to simulate
	// draw-to-draw variation.
	Draw int
	// Meta carries fake-backend hints (class, leaf id). Never sent to a
	// real model.
	Meta map[string]string
}

// Response is one completed call.
type Response struct {
	Text       string
	TokensIn   int
	TokensOut  int
	LatencyMs  int64 // wall time of the call including service-side queueing and SDK retries
	StopReason string
}

// Client makes on-demand calls.
type Client interface {
	Invoke(ctx context.Context, m config.Model, req Request) (Response, error)
}

// BatchItem is one record in a batch job.
type BatchItem struct {
	RecordID string
	Request  Request
}

// BatchResult is one record's outcome; Err is set for records the job
// failed on.
type BatchResult struct {
	Response Response
	Err      string
}

// BatchClient runs a set of requests for one model as a batch job and
// returns per-record results and the job's wall time.
type BatchClient interface {
	RunBatch(ctx context.Context, m config.Model, items []BatchItem) (map[string]BatchResult, int64, error)
}

// New returns the client configured by cfg.Backend. The returned value
// also implements BatchClient.
func New(ctx context.Context, cfg *config.Config) (interface {
	Client
	BatchClient
}, error) {
	switch cfg.Backend {
	case "fake":
		return NewFake(cfg), nil
	case "bedrock":
		return NewBedrock(ctx, cfg)
	}
	return nil, fmt.Errorf("unknown backend %q", cfg.Backend)
}
