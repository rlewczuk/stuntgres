package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgPrewarmLifecycle(t *testing.T) {
	t.Run("incarnations", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pw_life_owner"})
		pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			pwCacheInstall(t, p, b, c)
			bcDDL(t, p, b, c, "CREATE SCHEMA pw_moved", "CREATE ROLE pw_life_owner", "CREATE TABLE pw_part.unrelated(k integer)")
			pwInsert(t, p, b, c, "pw_part.heap", 1000)
			pwInsert(t, p, b, c, "pw_part.unrelated", 1)
			before := pwCoordinates(t, p, b, c, "pw_part.heap")
			st, err := c.PrepareContext(p.Ctx, pwCall)
			if err != nil { t.Fatal(err) }
			defer st.Close()
			h := fixture.HeldStatement{Executor: c, Statement: st, SQL: pwCall}
			if !b.PG { pwSameCoordinates(t, before, pwCoordinates(t, p, b, c, "pw_part.heap")) }
			fixture.RunStep(t, p.Ctx, h, b.Recorder, pwCount(pwCall, 2, "pw_part.heap", "buffer", "main", int64(1), int64(2)))
			pwResident(t, p, b, c, "pw_part.heap", 0, 1, 2)
			checkUsage := func(want int64) {
				if b.PG { return }
				for key, row := range pwCoordinates(t, p, b, c, "pw_part.heap") {
					if row[7] != want { t.Fatal("execution failed to touch real coordinate", key, row, want) }
				}
			}
			checkUsage(1)
			// Each direct/fresh/held call must perform work; planning cannot
			// replace a VOLATILE invocation with a retained literal count.
			for i, route := range []bool{false, true} {
				s := pwCount(pwCall, 2, "pw_part.heap", "buffer", "main", int64(1), int64(2))
				if route { s = fixture.Prepared(s) }
				bcRun(t, p, b, c, s)
				checkUsage(int64(i+2))
			}
			fixture.RunStep(t, p.Ctx, h, b.Recorder, pwCount(pwCall, 2, "pw_part.heap", "buffer", "main", int64(1), int64(2)))
			checkUsage(4)
			bcRun(t, p, b, c, pwCount(pwCall, 1, "pw_part.unrelated", "buffer", "main", nil, nil))
			other := pwCoordinates(t, p, b, c, "pw_part.unrelated")
			before = pwCoordinates(t, p, b, c, "pw_part.heap")
			node, space, database := bcStorage(t, p, c, "pw_part.heap")
			for _, ddl := range []string{"ALTER TABLE pw_part.heap RENAME TO kept", "ALTER TABLE pw_part.kept OWNER TO pw_life_owner", "GRANT SELECT ON pw_part.kept TO PUBLIC", "REVOKE SELECT ON pw_part.kept FROM PUBLIC", "ALTER TABLE pw_part.kept SET SCHEMA pw_moved"} {
				bcDDL(t, p, b, c, ddl)
				name := "pw_part.kept"
				if ddl == "ALTER TABLE pw_part.kept SET SCHEMA pw_moved" { name = "pw_moved.kept" }
				if !b.PG { pwSameCoordinates(t, before, pwCoordinates(t, p, b, c, name)) }
			}
			retired := func() {
				rows := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode=$1::oid AND reltablespace=$2::oid AND reldatabase=$3::oid", true, node, space, database)
				if !b.PG && len(rows) != 0 { t.Fatal("old incarnation retained coordinates", rows) }
				if !b.PG { pwSameCoordinates(t, other, pwCoordinates(t, p, b, c, "pw_part.unrelated")) }
			}
			bcDDL(t, p, b, c, "TRUNCATE pw_moved.kept")
			retired()
			fixture.RunStep(t, p.Ctx, h, b.Recorder, pwCount(pwCall, 0, "pw_moved.kept", "buffer", "main", nil, nil))
			pwInsert(t, p, b, c, "pw_moved.kept", 1000)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, pwCount(pwCall, 2, "pw_moved.kept", "buffer", "main", int64(1), int64(2)))
			node, space, database = bcStorage(t, p, c, "pw_moved.kept")
			bcDDL(t, p, b, c, "VACUUM FULL pw_moved.kept")
			retired()
			fixture.RunStep(t, p.Ctx, h, b.Recorder, pwCount(pwCall, 2, "pw_moved.kept", "buffer", "main", int64(1), int64(2)))
			node, space, database = bcStorage(t, p, c, "pw_moved.kept")
			bcDDL(t, p, b, c, "DROP TABLE pw_moved.kept", "CREATE TABLE pw_moved.kept(k integer)")
			retired()
			fixture.RunStep(t, p.Ctx, h, b.Recorder, pwCount(pwCall, 0, "pw_moved.kept", "buffer", "main", nil, nil))
		})
	})
	t.Run("cancellation", func(t *testing.T) {
		pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			pwCacheInstall(t, p, b, c)
			pwInsert(t, p, b, c, "pw_part.heap", 1)
			oid := fsOID(t, p, c, "pw_part.heap")
			q := "SELECT pw_part.pg_prewarm($1::oid::regclass) AS warmed"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer st.Close()
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := st.QueryContext(ctx, oid)
				if rows != nil { rows.Close(); t.Fatal("canceled call published rows") }
				fixture.LexicalOrdinary(t, err, context.Canceled)
			}
			holder, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer holder.Close()
			waiterPID, holderPID := 0, 0
			if b.PG { waiterPID = fixture.LockPID(t, p.Ctx, c); holderPID = fixture.LockPID(t, p.Ctx, holder) }
			bcDDL(t, p, b, holder, "BEGIN", "LOCK TABLE pw_part.heap IN ACCESS EXCLUSIVE MODE")
			defer holder.ExecContext(p.Ctx, "ROLLBACK")
			fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
				if b.PG { fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "AccessShareLock") }
			}, func(ctx context.Context) fixture.RelationOutcome {
				rows, err := st.QueryContext(ctx, oid)
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			bcDDL(t, p, b, holder, "ROLLBACK")
			bcRun(t, p, b, c, pwCount(q, 1, oid))
			pwResident(t, p, b, c, "pw_part.heap", 0, 0, 0)
			bcDDL(t, p, b, holder, "BEGIN", "SET LOCAL lock_timeout='1s'", "LOCK TABLE pw_part.heap IN ACCESS EXCLUSIVE MODE", "ROLLBACK")
		})
	})
	t.Run("private_publication", func(t *testing.T) {
		pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			pwCacheInstall(t, p, b, c)
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			bcDDL(t, p, b, c, "BEGIN", "CREATE TABLE pw_part.private_heap(k integer)")
			pwInsert(t, p, b, c, "pw_part.private_heap", 1)
			pwRoutes(t, p, b, c, pwCount(pwCall, 1, "pw_part.private_heap", "buffer", "main", nil, nil))
			if !b.PG && len(pwCoordinates(t, p, b, c, "pw_part.private_heap")) != 0 { t.Fatal("uncommitted new relation leaked shared coordinates") }
			bcDDL(t, p, b, c, "COMMIT")
			if !b.PG && len(pwCoordinates(t, p, b, c, "pw_part.private_heap")) != 0 { t.Fatal("commit published a private prewarm lease") }
			bcRun(t, p, b, c, pwCount(pwCall, 1, "pw_part.private_heap", "buffer", "main", nil, nil))
			pwResident(t, p, b, c, "pw_part.private_heap", 0, 0, 0)
			before := pwCoordinates(t, p, b, c, "pw_part.private_heap")
			node, space, database := bcStorage(t, p, c, "pw_part.private_heap")
			bcDDL(t, p, b, c, "BEGIN", "TRUNCATE pw_part.private_heap")
			pwInsert(t, p, b, c, "pw_part.private_heap", 1)
			pwRoutes(t, p, b, c, pwCount(pwCall, 1, "pw_part.private_heap", "buffer", "main", nil, nil))
			if !b.PG {
				pwSameCoordinates(t, before, pwCoordinates(t, p, b, c, "pw_part.private_heap"))
				old := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode=$1::oid AND reltablespace=$2::oid AND reldatabase=$3::oid", true, node, space, database)
				if len(old) != 1 || old[0][7] != int64(1) { t.Fatal("private work mutated committed cache", old) }
			}
			bcDDL(t, p, b, c, "ROLLBACK")
			if !b.PG { pwSameCoordinates(t, before, pwCoordinates(t, p, b, c, "pw_part.private_heap")) }
		})
	})
}
