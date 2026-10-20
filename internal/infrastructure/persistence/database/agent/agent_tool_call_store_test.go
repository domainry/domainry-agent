package agent

import (
	"context"
	"errors"
	"testing"
)

func TestAgentToolLedgerRetriesTransientStoreFailures(t *testing.T) {
	transient := errors.New("transient")
	attempts := 0
	err := retryAgentTaskStoreTransient(t.Context(), func(err error) bool {
		return errors.Is(err, transient)
	}, func() error {
		attempts++
		if attempts < 3 {
			return transient
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestAgentToolLedgerDoesNotRetryDeterministicFailures(t *testing.T) {
	deterministic := errors.New("deterministic")
	attempts := 0
	err := retryAgentTaskStoreTransient(t.Context(), func(error) bool { return false }, func() error {
		attempts++
		return deterministic
	})
	if !errors.Is(err, deterministic) || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestAgentToolLedgerStopsTransientRetryWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	err := retryAgentTaskStoreTransient(ctx, func(error) bool { return true }, func() error {
		attempts++
		cancel()
		return errors.New("transient")
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}
