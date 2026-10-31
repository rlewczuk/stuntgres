package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatindexCancellation(t *testing.T) {
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'ps_part.ps_index'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		q := "SELECT * FROM ps_part.pgstatindex($1::oid)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		// Only the native API guarantees ordinary context cancellation before
		// execution. A fast pre-canceled pq call may legitimately publish rows.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := st.QueryContext(ctx, oid)
			if rows != nil {
				rows.Close()
				t.Fatal("already canceled query published rows")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		waiterPID, holderPID := 0, 0
		if b.PG {
			waiterPID = fixture.LockPID(t, p.Ctx, c)
			holderPID = fixture.LockPID(t, p.Ctx, holder)
		}
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("BEGIN", 0))
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		// Unlike ALTER INDEX RENAME's ShareUpdateExclusive, transactional DROP
		// holds AccessExclusive on this exact index and conflicts with the
		// function's AccessShare. The uncommitted drop is invisible to the reader.
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("DROP INDEX ps_part.ps_index", 0))
		fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
			if b.PG {
				fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "AccessShareLock")
			}
		}, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := st.QueryContext(ctx, oid)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("ROLLBACK", 0))
		pgstatindexEmpty(t, p, c, q, oid)
		// A subsequent AccessShare read alone would not detect retained locks.
		// Reacquiring the exact index's AccessExclusive proves their release;
		// rollback also preserves its original identity and contents.
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("SET LOCAL lock_timeout='1s'", 0))
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("DROP INDEX ps_part.ps_index", 0))
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("ROLLBACK", 0))
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex($1::text)", "ps_part.ps_index")
	})
}
