package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"
)

func referenceTiesCanceledBeforeExecution(t *testing.T, ctx context.Context, db referenceExecutor, query string) {
	t.Helper()
	canceled, stop := context.WithCancel(ctx)
	stop()
	rows, err := db.QueryContext(canceled, query)
	if rows != nil {
		rows.Close()
		t.Fatal("canceled execution exposed cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled execution", err)
	}
}

func referenceTiesCanceledCursor(t *testing.T, rows *sql.Rows, postgres bool) {
	t.Helper()
	defer rows.Close()
	if postgres {
		// database/sql closes lib/pq cursors asynchronously on context expiry.
		// Observe that cancellation boundary without consuming any buffered row;
		// otherwise Next can win the race and mask the cancellation assertion.
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for rows.Err() == nil {
			select {
			case <-deadline.C:
				t.Fatal("cursor cancellation was not observed")
			default:
				runtime.Gosched()
			}
		}
	}
	if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal("public cancellation did not stick", rows.Err())
	}
	if rows.Next() {
		t.Fatal("canceled Next resumed SQL")
	}
}
