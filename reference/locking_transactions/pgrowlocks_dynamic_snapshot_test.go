package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// The correlated gate cannot run before reading the real outer storage row.
// MATERIALIZED and the argument's dependency on admitted keep the separately
// named target observation after the gate, without a timing or planner-order bet.
const pgrowDynamicSnapshotSQL = `WITH names AS MATERIALIZED (
 SELECT src.n,(SELECT count(*) FROM pgrowlocks(src.g)) AS admitted
 FROM pgrow_names src
)
SELECT r.* FROM names CROSS JOIN LATERAL
 pgrowlocks(CASE WHEN names.admitted=0 THEN names.n ELSE NULL::text END) AS r`

func TestReferencePgrowlocksDynamicSnapshot(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ"} {
		for _, route := range []struct {
			name     string
			stored   bool
			nested   bool
			prepared bool
		}{
			{name: "direct"},
			{name: "held-prepared", prepared: true},
			{name: "stored-view", stored: true},
			{name: "nested-stored-held-prepared", stored: true, nested: true, prepared: true},
		} {
			t.Run(isolation+"/"+route.name, func(t *testing.T) {
				pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
					pgrowTable(t, p, a)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE pgrow_gate(id INT)", 0)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE pgrow_names(n TEXT,g TEXT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO pgrow_names VALUES('pgrow_t','pgrow_gate')", 1)
					query := pgrowDynamicSnapshotSQL
					if route.stored {
						fixture.LockExec(t, p.Ctx, a, "CREATE VIEW pgrow_dynamic_saved AS "+query, 0)
						query = "SELECT * FROM pgrow_dynamic_saved"
					}
					if route.nested {
						fixture.LockExec(t, p.Ctx, a, "CREATE VIEW pgrow_dynamic_nested AS SELECT * FROM pgrow_dynamic_saved", 0)
						query = "SELECT nested.* FROM (SELECT * FROM pgrow_dynamic_nested) AS nested"
					}
					reader, gate := fixture.LockConnections(t, p.Ctx, b.DB)
					updater, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					var held *sql.Stmt
					if route.prepared {
						var err error
						held, err = reader.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { held.Close() })
					}
					run := func(ctx context.Context) fixture.RelationOutcome {
						if held != nil {
							rows, err := held.QueryContext(ctx)
							return fixture.RelationOutcome{Rows: rows, Err: err}
						}
						return fixture.RowQuery(ctx, reader, query, false)
					}

					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					survivor := pgrowOwner(t, p, a, "For Key Share")
					identityLock(t, p, a, "pgrow_t", 1, "KEY SHARE")
					old := pgrowOne(t, pgrowGet(t, p, observer), false, survivor)
					fixture.LockExec(t, p.Ctx, reader, "BEGIN ISOLATION LEVEL "+isolation, 0)
					readerPID := pgrowTransactionPID(t, p, reader, identityLow(t, identityID(t, p, reader)))
					// Prime RR on the OUTER source only, never the runtime target.
					// RC will acquire a new statement snapshot for the gated query.
					fixture.OidReferenceRows(t, p.Ctx, b, reader, fixture.Select("SELECT n,g FROM pgrow_names", []string{"n", "g"}, []string{"TEXT", "TEXT"}, [][]any{{"pgrow_t", "pgrow_gate"}}))
					fixture.LockExec(t, p.Ctx, gate, "BEGIN", 0)
					gatePID := pgrowTransactionPID(t, p, gate, identityLow(t, identityID(t, p, gate)))
					fixture.LockExec(t, p.Ctx, gate, "LOCK TABLE pgrow_gate IN ACCESS EXCLUSIVE MODE", 0)
					await := fixture.LockOperation(t, p.Ctx, run)
					pgrowDynamicGateWait(t, p, observer, readerPID, gatePID)

					// The statement has read the outer source and is parked inside
					// its correlated gate. Replace a DIFFERENT relation's version
					// through a real public update before the target SRF can run.
					fixture.LockExec(t, p.Ctx, updater, "BEGIN", 0)
					ended := pgrowOwner(t, p, updater, "No Key Update")
					fixture.LockExec(t, p.Ctx, updater, "UPDATE pgrow_t SET v=1 WHERE id=1", 1)
					mixed := pgrowOne(t, pgrowGet(t, p, observer), true, survivor, ended)
					pgrowSameTID(t, old, mixed)
					fixture.LockExec(t, p.Ctx, updater, "COMMIT", 0)
					ended.pid = 0
					current := pgrowOne(t, pgrowGet(t, p, observer), false, survivor)
					if current.tid == old.tid {
						t.Fatal("committed successor reused predecessor TID", current, old)
					}
					fixture.LockExec(t, p.Ctx, gate, "ROLLBACK", 0)
					out := await()
					if out.Err != nil {
						t.Fatal(query, out.Err)
					}
					captured := pgrowOne(t, pgrowRead(t, out.Rows), true, survivor, ended)
					pgrowSameTID(t, old, captured)
					if captured.locker != mixed.locker {
						t.Fatal("snapshot lost original raw mixed identity", captured, mixed)
					}

					// Reexecute the SAME consumer (and held handle). RC advances to
					// the successor; RR retains its predecessor and complete raw bag.
					out = run(p.Ctx)
					if out.Err != nil {
						t.Fatal(query, out.Err)
					}
					if isolation == "REPEATABLE READ" {
						again := pgrowOne(t, pgrowRead(t, out.Rows), true, survivor, ended)
						pgrowSameTID(t, captured, again)
						if again.locker != mixed.locker {
							t.Fatal("RR reexecution changed raw mixed identity", again, mixed)
						}
					} else {
						pgrowSameTID(t, current, pgrowOne(t, pgrowRead(t, out.Rows), false, survivor))
					}
					wantValue := int64(1)
					if isolation == "REPEATABLE READ" {
						wantValue = 0
					}
					fixture.OidReferenceRows(t, p.Ctx, b, reader, fixture.Select("SELECT v FROM pgrow_t", []string{"v"}, []string{"INT4"}, [][]any{{wantValue}}))
					fixture.OidReferenceRows(t, p.Ctx, b, observer, fixture.Select("SELECT v FROM pgrow_t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
					fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
					pgrowEmpty(t, p, reader)
					pgrowEmpty(t, p, observer)
					fixture.LockExec(t, p.Ctx, reader, "ROLLBACK", 0)
				})
			})
		}
	}
}

// Like pgrowWait, but the exact pending AccessShare request is on the empty
// gate relation, not the target. Both the request and actual blocker must exist.
func pgrowDynamicGateWait(t *testing.T, p *fixture.Pair, observer *sql.Conn, reader, holder int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	const query = `SELECT $2=ANY(pg_blocking_pids($1)) AND EXISTS(
 SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation'
 AND relation='pgrow_gate'::regclass AND mode='AccessShareLock' AND NOT granted)`
	for ctx.Err() == nil {
		var pending bool
		if err := observer.QueryRowContext(ctx, query, reader, holder).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("no actual dynamic snapshot gate request", ctx.Err())
}
