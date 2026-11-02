package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemRowsCancellation(t *testing.T) {
	srCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT id,payload FROM sr TABLESAMPLE system_rows($1)"
		h := srHeld(t, p, c, q)
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := h.Statement.QueryContext(ctx, int64(3))
			if rows != nil {
				rows.Close()
				t.Fatal("already canceled sample published rows")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'sr'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		waiterPID, holderPID := 0, 0
		if b.PG {
			waiterPID = fixture.LockPID(t, p.Ctx, c)
			holderPID = fixture.LockPID(t, p.Ctx, holder)
		}
		for _, prepared := range []bool{false, true} {
			srRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
			srRun(t, p, b, holder, fixture.Exec("LOCK TABLE sr IN ACCESS EXCLUSIVE MODE", 0))
			fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
				if b.PG {
					fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "AccessShareLock")
				}
			}, func(ctx context.Context) fixture.RelationOutcome {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = h.Statement.QueryContext(ctx, int64(3))
				} else {
					rows, err = c.QueryContext(ctx, q, int64(3))
				}
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			srRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
			srBoth(t, p, b, c, srSubset("sr", "system_rows", "3", 3))
			// Read success alone cannot detect retained AccessShare resources.
			srRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
			srRun(t, p, b, holder, fixture.Exec("LOCK TABLE sr IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
			srRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
		}
	})
}
