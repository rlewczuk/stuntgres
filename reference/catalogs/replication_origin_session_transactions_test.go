package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func originSessionTransactionWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, smoke bool) {
	t.Helper()
	originSessionDropAbortWitness(t, p, b)
	name := "session_transactions"
	originStateCleanup(t, b.DB, name)
	id := originStateCreate(t, p, b, b.DB, name)
	a := originSessionConn(t, p, b.DB)
	peer := originSessionConn(t, p, b.DB)
	run := func(s fixture.Step) { originStateRun(t, p, b, a, s) }
	run(fixture.Exec("CREATE TABLE session_tx_rows(v int)", 0))
	originSessionSetup(t, p, b, a, name)
	originSessionIsSetup(t, p, b, a, true)
	originSessionIsSetup(t, p, b, peer, false)
	setupSQL := "SELECT pg_replication_origin_session_setup($1::text) AS v"
	setupStmt, err := a.PrepareContext(p.Ctx, setupSQL)
	if err != nil {
		t.Fatal(err)
	}
	run(fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
	originSessionSetup(t, p, b, fixture.HeldStatement{Executor: a, Statement: setupStmt, SQL: setupSQL}, name)
	setupStmt.Close()
	originSessionIsSetup(t, p, b, a, true)
	q := "SELECT pg_replication_origin_xact_setup($1::pg_lsn,$2::timestamptz) AS v"
	stmt, err := a.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
	originStateRun(t, p, b, held, originStateScalar(q, "VOID", "", "0/10", "2022-03-04 05:06:07+00"))
	originSessionProgress(t, p, b, a, nil)
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO session_tx_rows VALUES(10)", 1))
	originStateProgress(t, p, b, peer, name, nil)
	run(fixture.Exec("COMMIT", 0))
	originSessionProgress(t, p, b, a, "0/10")
	run(fixture.Exec("BEGIN", 0))
	originSessionXact(t, p, b, a, "0/20")
	run(fixture.Exec("INSERT INTO session_tx_rows VALUES(20)", 1))
	run(fixture.Exec("ROLLBACK", 0))
	originStateProgress(t, p, b, peer, name, "0/20")
	run(originStateScalar("SELECT count(*) AS v FROM session_tx_rows WHERE v=20", "INT8", int64(0)))
	run(fixture.Exec("BEGIN", 0))
	originSessionXact(t, p, b, a, "0/30")
	run(fixture.Exec("ROLLBACK", 0))
	originSessionProgress(t, p, b, a, "0/20")
	run(fixture.Exec("INSERT INTO session_tx_rows VALUES(30)", 1))
	originSessionProgress(t, p, b, a, "0/30")
	originSessionXact(t, p, b, a, "0/1")
	run(fixture.Exec("INSERT INTO session_tx_rows VALUES(1)", 1))
	originSessionProgress(t, p, b, a, "0/30")
	for _, lsn := range []string{"80000000/0", "FFFFFFFF/FFFFFFFF"} {
		originSessionXact(t, p, b, a, lsn)
		run(fixture.Exec("SELECT pg_current_xact_id()", 1))
		originSessionProgress(t, p, b, a, lsn)
	}
	if smoke {
		pid := originStateNumber(t, p, a, "SELECT pg_backend_pid()")
		originStateRun(t, p, b, peer, originStateError("SELECT pg_replication_origin_session_setup($1::text)", "55006", fmt.Sprintf("replication origin with ID %d is already active for PID %d", id, pid), "", name))
		originStateRun(t, p, b, held, originStateScalar(q, "VOID", nil, nil, "2022-03-04+00"))
		run(fixture.Exec("SELECT pg_replication_origin_xact_reset()", 1))
		originSessionIsSetup(t, p, b, a, true)
		s := originStateScalar("SELECT pg_replication_origin_session_progress(true) AS v", "PG_LSN", "FFFFFFFF/FFFFFFFF")
		if !b.PG {
			s = originStateError(s.Query, "0A000", "pg_replication_origin_session_progress with flush=true requires durable WAL flushing, which is unsupported in embedded mode", "")
		}
		run(s)
		run(fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
		originSessionSetup(t, p, b, peer, name)
	}
}
func TestReferenceReplicationOriginSessionTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { originSessionTransactionWitness(t, p, b, false) })
	}
}
func TestReferenceReplicationOriginSessionFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { originSessionTransactionWitness(t, p, b, true) })
	}
}

func TestReferenceReplicationOriginSessionSubtransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name := "session_subtransactions"
			originStateCleanup(t, b.DB, name)
			originStateCreate(t, p, b, b.DB, name)
			a := originSessionConn(t, p, b.DB)
			observer := originSessionConn(t, p, b.DB)
			run := func(s fixture.Step) { originStateRun(t, p, b, a, s) }
			run(fixture.Exec("CREATE TABLE session_sub_rows(v int)", 0))
			originSessionSetup(t, p, b, a, name)
			// An assigned outer XID is not a child assignment, including top-only inquiry.
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO session_sub_rows VALUES(1)", 1))
			originSessionXact(t, p, b, a, "0/40")
			run(fixture.Exec("SAVEPOINT s", 0))
			run(fixture.Exec("SELECT pg_current_xact_id()", 1))
			run(fixture.Exec("ROLLBACK TO s", 0))
			originStateProgress(t, p, b, observer, name, nil)
			run(fixture.Exec("INSERT INTO session_sub_rows VALUES(2)", 1))
			originSessionXact(t, p, b, a, "0/50")
			run(fixture.Exec("ROLLBACK TO s", 0))
			originSessionProgress(t, p, b, a, "0/50")
			originSessionXact(t, p, b, a, "0/60")
			run(fixture.Exec("ROLLBACK TO s", 0))
			originSessionProgress(t, p, b, a, "0/50")
			run(fixture.Exec("INSERT INTO session_sub_rows VALUES(3)", 1))
			run(fixture.Exec("ROLLBACK TO s", 0))
			originSessionProgress(t, p, b, a, "0/60")
			run(fixture.Exec("ROLLBACK", 0))
			originSessionProgress(t, p, b, a, "0/60")
			originStateRun(t, p, b, observer, originStateScalar("SELECT count(*) AS v FROM session_sub_rows", "INT8", int64(0)))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT parent", 0))
			run(fixture.Exec("SAVEPOINT child", 0))
			originSessionXact(t, p, b, a, "0/70")
			run(fixture.Exec("INSERT INTO session_sub_rows VALUES(7)", 1))
			run(fixture.Exec("RELEASE child", 0))
			originSessionProgress(t, p, b, a, "0/60")
			run(fixture.Exec("ROLLBACK TO parent", 0))
			originSessionProgress(t, p, b, a, "0/70")
			run(fixture.Exec("ROLLBACK", 0))
			// Exception marks are actual subtransactions; configured globals are not part
			// of their rollback backing, while row writes are.
			run(fixture.Exec("DO $$BEGIN BEGIN PERFORM pg_replication_origin_xact_setup('0/80','2022-03-04+00'); INSERT INTO session_sub_rows VALUES(8); RAISE EXCEPTION 'origin child'; EXCEPTION WHEN OTHERS THEN PERFORM 1; END; END$$", 0))
			originSessionProgress(t, p, b, a, "0/80")
			run(fixture.Exec("INSERT INTO session_sub_rows VALUES(9)", 1))
			originSessionProgress(t, p, b, a, "0/80")
			run(originStateScalar("SELECT count(*) AS v FROM session_sub_rows", "INT8", int64(1)))
			// No-XID child changes persist, without publishing a pending position.
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT s", 0))
			originSessionXact(t, p, b, a, "0/90")
			run(fixture.Exec("ROLLBACK TO s", 0))
			originSessionProgress(t, p, b, a, "0/80")
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("INSERT INTO session_sub_rows VALUES(10)", 1))
			originSessionProgress(t, p, b, a, "0/90")
		})
	}
}

// Keep the distinguishing DROP paths on their own origins so their 0/90
// advancement cannot mask the transaction family's earlier 0/20 and 0/30 cases.
func originSessionDropAbortWitness(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	names := []string{"session_drop_unassigned", "session_drop_assigned_direct", "session_drop_assigned_held"}
	originStateCleanup(t, b.DB, names...)
	a := originSessionConn(t, p, b.DB)
	observer := originSessionConn(t, p, b.DB)
	originStateRun(t, p, b, a, fixture.Exec("CREATE TABLE session_drop_rows(v integer)", 0))
	q := "SELECT pg_replication_origin_drop($1::text)"
	stmt, err := a.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
	pid := originStateNumber(t, p, a, "SELECT pg_backend_pid()")
	for i, name := range names {
		id := originStateCreate(t, p, b, observer, name)
		originSessionSetup(t, p, b, a, name)
		originSessionXact(t, p, b, a, "0/10")
		originStateRun(t, p, b, a, fixture.Exec("INSERT INTO session_drop_rows VALUES($1)", 1, 10+i))
		originSessionProgress(t, p, b, a, "0/10")
		originSessionXact(t, p, b, a, "0/90")
		rejected := originStateError(q, "55006", fmt.Sprintf("could not drop replication origin with ID %d, in use by PID %d", id, pid), "", name)
		if i == 0 {
			originStateRun(t, p, b, a, rejected)
			originSessionProgress(t, p, b, a, "0/10")
			originStateRun(t, p, b, held, rejected)
			originSessionProgress(t, p, b, a, "0/10")
			originSessionIsSetup(t, p, b, a, true)
			originStateRun(t, p, b, a, fixture.Exec("INSERT INTO session_drop_rows VALUES(90)", 1))
			originSessionProgress(t, p, b, a, "0/90")
		} else {
			var actor fixture.Executor = a
			if i == 2 {
				actor = held
			}
			value := 190 + i
			originStateRun(t, p, b, a, fixture.Exec("BEGIN", 0))
			originStateRun(t, p, b, a, fixture.Exec("INSERT INTO session_drop_rows VALUES($1)", 1, value))
			originStateRun(t, p, b, actor, rejected)
			originStateProgress(t, p, b, observer, name, "0/90")
			originStateRun(t, p, b, observer, originStateScalar("SELECT count(*) AS v FROM session_drop_rows WHERE v=$1", "INT8", int64(0), value))
			originStateRun(t, p, b, a, fixture.Exec("ROLLBACK", 0))
			originSessionIsSetup(t, p, b, a, true)
		}
		originStateRun(t, p, b, a, fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
	}
}
