//go:build reference

package reference_test

import (
	"context"
	"testing"
	"time"
)

func referenceRowExecute(ctx context.Context, x referenceExecutor, query string, prepared bool) relationOutcome {
	var outcome relationOutcome
	if prepared {
		outcome.stmt, outcome.err = x.PrepareContext(ctx, query)
		if outcome.err == nil {
			outcome.result, outcome.err = outcome.stmt.ExecContext(ctx)
		}
		if outcome.stmt != nil {
			outcome.stmt.Close()
			outcome.stmt = nil
		}
	} else {
		outcome.result, outcome.err = x.ExecContext(ctx, query)
	}
	return outcome
}
func referenceRowAsync(t *testing.T, ctx context.Context, x referenceExecutor, query string, prepared, selectRows bool) <-chan relationOutcome {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	done := make(chan relationOutcome, 1)
	go func() {
		var outcome relationOutcome
		if selectRows {
			outcome = referenceRowQuery(bounded, x, query, prepared)
		} else {
			outcome = referenceRowExecute(bounded, x, query, prepared)
		}
		done <- outcome
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case outcome := <-done:
			if outcome.rows != nil {
				outcome.rows.Close()
			}
			if outcome.stmt != nil {
				outcome.stmt.Close()
			}
		case <-time.After(5 * time.Second):
			t.Error("row-lock asynchronous operation did not join")
		}
	})
	return done
}
func referenceRowAffected(t *testing.T, ctx context.Context, x referenceExecutor, query string, prepared bool, count int64) {
	t.Helper()
	outcome := referenceRowExecute(ctx, x, query, prepared)
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	referenceLockAffected(t, outcome.result, count)
}
