// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package llm

import (
	"context"
	"errors"

	"github.com/scttfrdmn/sluice/internal/config"
)

// Local is the open-weight lane: an OpenAI-compatible endpoint (vLLM) on
// local hardware, behind the same Client interface. It is stubbed for the
// first E0 run (PROJECT.md §6) and is enabled only by a non-empty
// config.local_models.
type Local struct{}

// ErrLocalLaneDisabled is returned until the lane is implemented.
var ErrLocalLaneDisabled = errors.New("local open-weight lane is not enabled for this run (PROJECT.md §6)")

// Invoke is not implemented yet.
func (Local) Invoke(context.Context, config.Model, Request) (Response, error) {
	return Response{}, ErrLocalLaneDisabled
}
