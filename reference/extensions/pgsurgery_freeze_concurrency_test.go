package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryFreezeConcurrency(t *testing.T) {
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
		freezeRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.unrelated(k integer)", 0))
		holder := freezeConn(t, p, b)
		defer holder.Close()
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'fs_part.heap'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		st, err := c.PrepareContext(p.Ctx, freezeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		waiterPID, holderPID := 0, 0
		if b.PG {
			waiterPID = fixture.LockPID(t, p.Ctx, c)
			holderPID = fixture.LockPID(t, p.Ctx, holder)
		}
		freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
		freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE fs_part.heap IN ACCESS EXCLUSIVE MODE", 0))
		fixture.BinaryConversionActiveCancelAfter(t, p, b, c, freezeSQL, "(*relationRequest).wait", func(ctx context.Context) {
			if b.PG {
				fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "RowExclusiveLock")
			}
		}, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := st.QueryContext(ctx, "fs_part.heap", freezeTIDs(a.tid))
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		freezeRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
		// The canceled borrower may have lost its pq transport. Use a fresh
		// lease for both the no-publication and later AX resource witnesses.
		reader := freezeConn(t, p, b)
		defer reader.Close()
		freezeExpect(t, p, b, reader, a)
		freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
		freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE fs_part.heap IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
		freezeRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
		freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
		freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE fs_part.unrelated IN ACCESS EXCLUSIVE MODE", 0))
		freezeRun(t, p, b, holder, freezeVoid("SELECT pg_advisory_xact_lock(987654321::bigint) AS repaired"))
		freezeRun(t, p, b, holder, fixture.Select("SELECT k FROM fs_part.heap WHERE k=1 FOR UPDATE", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		// No peer commit/release is needed: tuple ownership is deliberately
		// bypassed. Relation and advisory tickets must remain with their owner.
		freezeApply(t, p, b, reader, a.tid)
		freezeExpect(t, p, b, reader, freezeRepaired(a))
		freezeRun(t, p, b, holder, fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE pid=pg_backend_pid() AND granted AND ((locktype='relation' AND relation='fs_part.unrelated'::regclass AND mode='AccessExclusiveLock') OR locktype='advisory')", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		freezeRun(t, p, b, reader, fixture.Exec("BEGIN", 0))
		freezeRun(t, p, b, reader, fixture.ImplicitError("LOCK TABLE fs_part.unrelated IN ACCESS SHARE MODE NOWAIT", fixture.ControlsReferenceDiagnostic("55P03", "could not obtain lock on relation \"fs_part.unrelated\"")))
		freezeRun(t, p, b, reader, fixture.Exec("ROLLBACK", 0))
		freezeRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
		freezeExpect(t, p, b, reader, freezeRepaired(a))
		freezeRun(t, p, b, reader, fixture.Exec("BEGIN", 0))
		freezeRun(t, p, b, reader, fixture.Exec("LOCK TABLE fs_part.unrelated IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
		freezeRun(t, p, b, reader, fixture.Exec("ROLLBACK", 0))
	})
	freezePostrepairLockCases(t)
}
