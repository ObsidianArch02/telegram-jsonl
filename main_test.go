// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"errors"
	"testing"
)

func TestClassifyArchiveExitTreatsSignalAsCleanStop(t *testing.T) {
	result, signaled := classifyArchiveExit(context.Canceled, context.Canceled, nil)
	if result != nil || !signaled {
		t.Fatalf("signal cancellation was not classified as a clean stop: result=%v signaled=%t", result, signaled)
	}
}

func TestClassifyArchiveExitPreservesFailure(t *testing.T) {
	failure := errors.New("SQLite write failed")
	result, signaled := classifyArchiveExit(nil, nil, failure)
	if !errors.Is(result, failure) || signaled {
		t.Fatalf("archive failure was not preserved: result=%v signaled=%t", result, signaled)
	}
}

func TestClassifyArchiveExitRejectsUnexpectedNil(t *testing.T) {
	result, signaled := classifyArchiveExit(nil, nil, nil)
	if !errors.Is(result, errUnexpectedArchiveStop) || signaled {
		t.Fatalf("unexpected nil exit was not rejected: result=%v signaled=%t", result, signaled)
	}
}
