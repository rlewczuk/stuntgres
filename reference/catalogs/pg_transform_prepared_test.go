package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func pgTransformCanceled(t *testing.T, p *fixture.Pair, b fixture.Backend, held fixture.HeldStatement) {
	t.Helper()
	if b.PG {
		return // pq transport does not promise the native precancellation contract.
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err := b.DB.QueryContext(ctx, pgTransformOIDProbe, int64(0))
	if rows != nil {
		rows.Close()
		t.Fatal("canceled query returned Rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("query cancellation", err)
	}
	stmt, err := b.DB.PrepareContext(ctx, pgTransformOIDProbe)
	if stmt != nil {
		stmt.Close()
		t.Fatal("canceled prepare returned Stmt")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("prepare cancellation", err)
	}
	rows, err = held.QueryContext(ctx, pgTransformOIDProbe, int64(0))
	if rows != nil {
		rows.Close()
		t.Fatal("canceled held query returned Rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("held cancellation", err)
	}
}

func TestReferencePGTransformPrepared(t *testing.T) {
	pgTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		_, conn := fixture.LockConnections(t, p.Ctx, b.DB)
		for _, tc := range []struct {
			q    string
			args [][]any
		}{
			{pgTransformOIDProbe, [][]any{{int64(0)}, {int64(4294967295)}, {nil}}},
			{pgTransformPairProbe, [][]any{{int64(23), int64(12)}, {int64(0), int64(0)}, {int64(4294967295), int64(4294967295)}, {nil, int64(12)}, {int64(23), nil}}},
		} {
			stmt, err := conn.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.q}
			if tc.q == pgTransformOIDProbe {
				pgTransformCanceled(t, p, b, held)
			}
			for _, args := range tc.args {
				s := pgTransformStep(b, tc.q, args...)
				pgTransformBoth(t, p, b, conn, s)
				fixture.PgDescriptionExecutorRows(t, p, b, held, s)
			}
			name := "ptr_prepared_oid"
			if tc.q == pgTransformPairProbe {
				name = "ptr_prepared_pair"
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE public."+name+"(v INT)", 0))
			fixture.PgDescriptionExecutorRows(t, p, b, held, pgTransformStep(b, tc.q, tc.args[0]...))
		}
	})
}
