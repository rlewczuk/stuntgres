package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/session"
)

type deleteOutcome struct {
	result Result
	err    error
}

func deleteAsync(t *testing.T, s *session.Session, ctx context.Context, query string) <-chan deleteOutcome {
	t.Helper()
	ast := statement(t, query)
	done := make(chan deleteOutcome, 1)
	go func() {
		op, err := s.Acquire(ctx)
		var result Result
		if err == nil {
			result, err = EngineOf(s).Execute(op, ast, 0, nil)
			op.Done()
		}
		done <- deleteOutcome{result, err}
	}()
	return done
}

func deleteAwait(t *testing.T, done <-chan deleteOutcome, count int64, want error) {
	t.Helper()
	select {
	case got := <-done:
		if !errors.Is(got.err, want) || got.result.Rows != nil || got.result.Count != count {
			t.Fatal(got, count, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DELETE did not finish")
	}
}
