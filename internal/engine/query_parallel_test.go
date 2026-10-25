package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

type parallelTestOutcome struct {
	rows  exec.RowResult
	count int
	err   error
}

// Every exit, including a failed assertion at a gate, cancels and joins the
// real executor before fixture cleanup. No timing-based concurrency assertion.
func parallelTestStart(t *testing.T, call func(context.Context) parallelTestOutcome) (context.Context, context.CancelFunc, func() parallelTestOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan parallelTestOutcome, 1)
	go func() { done <- call(ctx) }()
	var once sync.Once
	var out parallelTestOutcome
	join := func() parallelTestOutcome {
		once.Do(func() {
			select {
			case out = <-done:
			case <-time.After(6 * time.Second):
				t.Error("parallel executor did not join")
				out.err = context.DeadlineExceeded
			}
		})
		return out
	}
	t.Cleanup(func() {
		cancel()
		out := join()
		if out.rows != nil {
			out.rows.Close()
		}
	})
	return ctx, cancel, join
}

func parallelTestIDs(t *testing.T, rows exec.RowResult) []int64 {
	t.Helper()
	defer rows.Close()
	values := make([]sqltype.Value, len(rows.Schema()))
	var ids []int64
	for {
		present, err := rows.Next(context.Background(), values)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			return ids
		}
		id, err := values[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
}
