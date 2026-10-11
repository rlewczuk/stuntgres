package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteReturningCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.LockExec(t, p.Ctx, backend.DB, "CREATE TABLE t(id INT)", 0)
			fixture.LockExec(t, p.Ctx, backend.DB, "INSERT INTO t VALUES(1),(2)", 2)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := backend.DB.QueryContext(ctx, "DELETE FROM t RETURNING *")
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
			fixture.DeleteReferenceRows(t, p, backend.DB, backend.Recorder, 1, 2)
		})
	}
}
