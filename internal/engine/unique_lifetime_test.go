package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

func uniqueAsyncUnit(t *testing.T, s *session.Session, query string) (<-chan deleteOutcome, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ast := statement(t, query)
	done := make(chan deleteOutcome, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		op, _, err := s.AcquireCommand(ctx)
		var result core.Result
		if err == nil {
			result, err = core.EngineOf(s).Execute(op, ast, 0, nil)
			err = op.CommandError(err)
			op.Done()
		}
		done <- deleteOutcome{result, err}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("UNIQUE worker did not join")
		}
	})
	return done, cancel
}

func uniqueClaimsEmpty(t *testing.T, e *core.Engine) {
	t.Helper()
	lockEmpty(t, e)
	uniqueKeyBucketsEmpty(t, e)
}

func uniqueKeyBucketsEmpty(t *testing.T, e *core.Engine) {
	t.Helper()
	lockState := e.Locks.Inspect()
	if lockState.KeyBuckets != 0 {
		t.Fatal("retained unique key buckets", lockState.KeyBuckets)
	}
}
