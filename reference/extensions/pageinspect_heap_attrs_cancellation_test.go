package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func attrsCancellation(t *testing.T) {
	attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'attrs_schema'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		q := "SELECT heap_part.tuple_data_split($1::oid,$2::bytea,0,2,NULL) AS attrs"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		waiterPID, holderPID := 0, 0
		if b.PG {
			waiterPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, holder, b.Recorder, s) }
		run(fixture.Exec("BEGIN", 0))
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("LOCK TABLE attrs_schema IN ACCESS EXCLUSIVE MODE", 0))
		fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
			if b.PG {
				fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "AccessShareLock")
			}
		}, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := st.QueryContext(ctx, oid, attrsBytes("0102030405060708"))
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		run(fixture.Exec("ROLLBACK", 0))
		heapBoth(t, p, b, c, attrsValue(q, attrsArray("01020304", "05060708"), oid, attrsBytes("0102030405060708")))
		// A new exclusive lock, not merely another shared read, proves release.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET LOCAL lock_timeout='1s'", 0))
		run(fixture.Exec("LOCK TABLE attrs_schema IN ACCESS EXCLUSIVE MODE", 0))
		run(fixture.Exec("ROLLBACK", 0))
	})
}
