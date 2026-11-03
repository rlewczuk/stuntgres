package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var originSessionFunctions = []string{"pg_replication_origin_session_setup", "pg_replication_origin_session_reset", "pg_replication_origin_session_is_setup", "pg_replication_origin_session_progress", "pg_replication_origin_xact_setup", "pg_replication_origin_xact_reset"}
var originSessionSignatures = []string{"text", "", "", "boolean", "pg_lsn,timestamptz", ""}

func originSessionConn(t *testing.T, p *fixture.Pair, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		if _, err := c.ExecContext(ctx, "ROLLBACK"); err != nil {
			t.Error(err)
		}
		var selected bool
		if err := c.QueryRowContext(ctx, "SELECT pg_replication_origin_session_is_setup()").Scan(&selected); err != nil {
			t.Error(err)
		} else if selected {
			if _, err := c.ExecContext(ctx, "SELECT pg_replication_origin_session_reset()"); err != nil {
				t.Error(err)
			}
		}
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}
func originSessionSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, name string) {
	t.Helper()
	originStateRun(t, p, b, c, originStateScalar("SELECT pg_replication_origin_session_setup($1::text) AS v", "VOID", "", name))
}
func originSessionIsSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, want bool) {
	t.Helper()
	originStateRun(t, p, b, c, originStateScalar("SELECT pg_replication_origin_session_is_setup() AS v", "BOOL", want))
}
func originSessionProgress(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, want any) {
	t.Helper()
	originStateRun(t, p, b, c, originStateScalar("SELECT pg_replication_origin_session_progress(false) AS v", "PG_LSN", want))
}
func originSessionXact(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, lsn string) {
	t.Helper()
	originStateRun(t, p, b, c, fixture.Exec("SELECT pg_replication_origin_xact_setup($1::pg_lsn,'2022-03-04 05:06:07+00'::timestamptz)", 1, lsn))
}
func TestReferenceReplicationOriginSessionLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name := "session_lifecycle"
			originStateCleanup(t, b.DB, name)
			originStateCreate(t, p, b, b.DB, name)
			a := originSessionConn(t, p, b.DB)
			peer := originSessionConn(t, p, b.DB)
			stateSQL := "SELECT pg_replication_origin_session_is_setup() AS v"
			stateStmt, err := a.PrepareContext(p.Ctx, stateSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer stateStmt.Close()
			stateHeld := fixture.HeldStatement{Executor: a, Statement: stateStmt, SQL: stateSQL}
			originSessionIsSetup(t, p, b, stateHeld, false)
			originSessionIsSetup(t, p, b, peer, false)
			q := "SELECT pg_replication_origin_session_setup($1::text) AS v"
			stmt, err := a.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
			originSessionSetup(t, p, b, held, name)
			originSessionIsSetup(t, p, b, stateHeld, true)
			originSessionIsSetup(t, p, b, peer, false)
			originSessionProgress(t, p, b, a, nil)
			originSessionXact(t, p, b, a, "0/10")
			originSessionProgress(t, p, b, a, nil)
			originStateRun(t, p, b, a, fixture.Exec("BEGIN", 0))
			originStateRun(t, p, b, a, fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
			originStateRun(t, p, b, a, fixture.Exec("ROLLBACK", 0))
			originSessionIsSetup(t, p, b, stateHeld, false)
			originStateRun(t, p, b, a, fixture.Exec("BEGIN", 0))
			originSessionSetup(t, p, b, held, name)
			originStateRun(t, p, b, a, fixture.Exec("ROLLBACK", 0))
			originSessionIsSetup(t, p, b, stateHeld, true)
			originStateRun(t, p, b, a, fixture.Exec("BEGIN", 0))
			originStateRun(t, p, b, a, fixture.Exec("SAVEPOINT s", 0))
			originStateRun(t, p, b, a, fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
			originStateRun(t, p, b, a, fixture.Exec("ROLLBACK TO s", 0))
			originSessionIsSetup(t, p, b, a, false)
			originStateRun(t, p, b, a, fixture.Exec("ROLLBACK", 0))
			originSessionSetup(t, p, b, peer, name)
		})
	}
}

func TestReferenceReplicationOriginSessionMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			names := []string{"oid", "proname", "result", "inputs", "proisstrict", "volatility", "parallel", "proretset", "prosecdef", "names", "pronargdefaults", "language"}
			types := []string{"INT8", "NAME", "INT8", "TEXT", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "INT2", "INT8"}
			rows := [][]any{}
			for i, fn := range originSessionFunctions {
				result := int64(2278)
				if i == 2 {
					result = 16
				}
				if i == 3 {
					result = 3220
				}
				inputs := []string{"25", "", "", "16", "3220 1184", ""}[i]
				parallel := []string{"u", "u", "r", "u", "r", "r"}[i]
				rows = append(rows, []any{int64(6006 + i), fn, result, inputs, true, "v", parallel, false, false, nil, int64(0), int64(12)})
			}
			q := `SELECT oid::bigint AS oid,proname,prorettype::bigint AS result,proargtypes::text AS inputs,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,proretset,prosecdef,proargnames::text AS names,pronargdefaults,prolang::bigint AS language FROM pg_proc WHERE oid BETWEEN 6006 AND 6011 ORDER BY oid`
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, names, types, rows)
				s.Prepared = prepared
				originStateRun(t, p, b, b.DB, s)
			}
			originStateCleanup(t, b.DB, "session_metadata")
			originStateCreate(t, p, b, b.DB, "session_metadata")
			c := originSessionConn(t, p, b.DB)
			calls := []string{"pg_replication_origin_session_setup('session_metadata')", "pg_replication_origin_session_reset()", "pg_replication_origin_session_is_setup()", "pg_replication_origin_session_progress(false)", "pg_replication_origin_xact_setup('0/1','2022-03-04+00')", "pg_replication_origin_xact_reset()"}
			for i, call := range calls {
				if i == 3 {
					originSessionSetup(t, p, b, c, "session_metadata")
				}
				typ := []string{"VOID", "VOID", "BOOL", "PG_LSN", "VOID", "VOID"}[i]
				var value any = ""
				if i == 2 {
					value = false
				}
				if i == 3 {
					value = nil
				}
				originStateRun(t, p, b, c, fixture.Select("SELECT "+call, []string{originSessionFunctions[i]}, []string{typ}, [][]any{{value}}))
				if i == 0 {
					originStateRun(t, p, b, c, fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
				}
				if i == 1 {
					originSessionSetup(t, p, b, c, "session_metadata")
				}
				originStateRun(t, p, b, c, fixture.Select("SELECT * FROM "+call+" AS f(v)", []string{"v"}, []string{typ}, [][]any{{value}}))
				originStateRun(t, p, b, c, fixture.Select("SELECT * FROM "+call+" AS f(v) LIMIT 0", []string{"v"}, []string{typ}, nil))
			}
		})
	}
}

func TestReferenceReplicationOriginSessionOwnership(t *testing.T) {
	databases := []string{"origin_ownership_db"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name := "session_ownership"
			originStateCleanup(t, b.DB, name)
			id := originStateCreate(t, p, b, b.DB, name)
			observer := originSessionConn(t, p, b.DB)
			originStateRun(t, p, b, observer, fixture.Exec("CREATE DATABASE origin_ownership_db", 0))
			pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "origin_ownership_db", "")
			pool.SetMaxOpenConns(1)
			pool.SetMaxIdleConns(1)
			ownerBackend := b
			ownerBackend.DB, ownerBackend.Recorder = pool, recorder
			c, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			originStateRun(t, p, ownerBackend, c, fixture.Exec("CREATE TABLE session_ownership_rows(v integer)", 0))
			originSessionSetup(t, p, ownerBackend, c, name)
			pid := originStateNumber(t, p, c, "SELECT pg_backend_pid()")
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			c, err = pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if got := originStateNumber(t, p, c, "SELECT pg_backend_pid()"); got != pid {
				t.Fatal("borrower did not reacquire its physical session", got, pid)
			}
			originSessionIsSetup(t, p, ownerBackend, c, true)
			originStateRun(t, p, ownerBackend, c, fixture.Exec("DISCARD ALL", 0))
			originSessionIsSetup(t, p, ownerBackend, c, true)
			originSessionXact(t, p, ownerBackend, c, "0/10")
			originStateRun(t, p, ownerBackend, c, fixture.Exec("INSERT INTO session_ownership_rows VALUES(10)", 1))
			originSessionProgress(t, p, ownerBackend, c, "0/10")
			originSessionXact(t, p, ownerBackend, c, "0/90")
			active := fmt.Sprintf("replication origin with ID %d is already active for PID %d", id, pid)
			for _, prepared := range []bool{false, true} {
				setup := originStateError("SELECT pg_replication_origin_session_setup($1::text)", "55006", active, "", name)
				setup.Prepared = prepared
				originStateRun(t, p, b, observer, setup)
				for _, q := range []string{"SELECT pg_replication_origin_advance($1::text,'0/9')", "SELECT pg_replication_origin_drop($1::text)"} {
					message := active
					if q == "SELECT pg_replication_origin_drop($1::text)" {
						message = fmt.Sprintf("could not drop replication origin with ID %d, in use by PID %d", id, pid)
					}
					rejected := originStateError(q, "55006", message, "", name)
					rejected.Prepared = prepared
					originStateRun(t, p, ownerBackend, c, rejected)
					originStateRun(t, p, b, observer, rejected)
				}
			}
			originStateProgress(t, p, b, observer, name, "0/10")
			// Rejection must retain the larger pending context, not just the slot.
			originStateRun(t, p, ownerBackend, c, fixture.Exec("INSERT INTO session_ownership_rows VALUES(90)", 1))
			originSessionProgress(t, p, ownerBackend, c, "0/90")
			originStateRun(t, p, ownerBackend, c, originStateScalar("SELECT count(*) AS v FROM session_ownership_rows", "INT8", int64(2)))
			// Pool close is a real physical close, unlike returning the borrower.
			// Session advisory cleanup provides an exit barrier without polling.
			originStateRun(t, p, ownerBackend, c, fixture.Exec("SELECT pg_advisory_lock(782691)", 1))
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := pool.Close(); err != nil {
				t.Fatal(err)
			}
			originStateRun(t, p, b, observer, fixture.Exec("SELECT pg_advisory_lock(782691)", 1))
			originStateRun(t, p, b, observer, originStateScalar("SELECT pg_advisory_unlock(782691) AS v", "BOOL", true))
			originSessionSetup(t, p, b, observer, name)
			originSessionProgress(t, p, b, observer, "0/90")
		})
	}
}
