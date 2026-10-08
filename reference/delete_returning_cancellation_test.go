package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceDeleteReturningCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceLockExec(t, p.ctx, backend.db, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, backend.db, "INSERT INTO t VALUES(1),(2)", 2)
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := backend.db.QueryContext(ctx, "DELETE FROM t RETURNING *")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query exposed cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary cancellation fabricated SQLSTATE", err)
			}
			deleteReferenceRows(t, p, backend.db, backend.recorder, 1, 2)
		})
	}
}
