package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAdvisorySessionTransactionPooling(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			for _, k := range advisorySessionKeys {
				for _, ending := range []string{"COMMIT", "ROLLBACK", "ROLLBACK TO advisory_savepoint"} {
					advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
					advisorySessionExec(t, p.Ctx, a, "SAVEPOINT advisory_savepoint", 0)
					advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock"), "VOID", "")
					advisorySessionExec(t, p.Ctx, a, ending, 0)
					if ending == "ROLLBACK TO advisory_savepoint" {
						advisorySessionExec(t, p.Ctx, a, "COMMIT", 0)
					}
					advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
					advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_unlock"), "BOOL", true)
				}
				advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock"), "VOID", "")
				advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
				advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_unlock"), "BOOL", true)
				advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
				advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", true)
				advisorySessionClear(t, p.Ctx, peer)
				advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock"), "VOID", "")
				advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
				advisorySessionError(t, p.Ctx, a, "SELECT 1/0", advisorySessionTimeout("22012", "division by zero"))
				advisorySessionError(t, p.Ctx, a, "SELECT true", advisorySessionTimeout("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
				advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
				advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				advisorySessionClear(t, p.Ctx, a)
			}
			advisorySessionExec(t, p.Ctx, a, "CREATE TABLE advisory_atomic(id integer NOT NULL)", 0)
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_lock(69)", "VOID", "")
			want := advisorySessionTimeout("23502", `null value in column "id" of relation "advisory_atomic" violates not-null constraint`)
			want.Schema, want.Table, want.Column, want.Detail = "public", "advisory_atomic", "id", "Failing row contains (null)."
			advisorySessionError(t, p.Ctx, a, "INSERT INTO advisory_atomic VALUES(1),(NULL)", want)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT id FROM advisory_atomic", []string{"id"}, []string{"INT4"}, nil))
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(69)", "BOOL", false)
			advisorySessionClear(t, p.Ctx, a)
			// Runtime dependency requires acquisition before the bad table
			// value cast, without assuming target-list/constant-fold ordering.
			advisorySessionExec(t, p.Ctx, a, "CREATE TABLE advisory_runtime_error(v text)", 0)
			advisorySessionExec(t, p.Ctx, a, "INSERT INTO advisory_runtime_error VALUES('bad')", 1)
			advisorySessionError(t, p.Ctx, a, "SELECT CASE WHEN pg_try_advisory_lock(68) THEN v::integer ELSE 0 END FROM advisory_runtime_error", advisorySessionTimeout("22P02", `invalid input syntax for type integer: "bad"`))
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(68)", "BOOL", false)
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_unlock(68)", "BOOL", true)
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(68)", "BOOL", true)
			advisorySessionClear(t, p.Ctx, peer)
			// All owner modes/keys/counts, but neither peer nor ordinary relation.
			advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
			advisorySessionExec(t, p.Ctx, a, "LOCK TABLE advisory_atomic IN ACCESS EXCLUSIVE MODE", 0)
			for _, k := range advisorySessionKeys {
				for _, name := range []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_advisory_lock"} {
					advisorySessionValue(t, p.Ctx, a, k.call(name), "VOID", "")
				}
			}
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_advisory_lock(70)", "VOID", "")
			advisorySessionClear(t, p.Ctx, a)
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_try_advisory_lock(70)", "BOOL", false)
			advisorySessionExec(t, p.Ctx, peer, "SET lock_timeout='40ms'", 0)
			want = advisorySessionTimeout("55P03", "canceling statement due to lock timeout")
			want.Position = "15"
			advisorySessionError(t, p.Ctx, peer, "SELECT * FROM advisory_atomic", want)
			advisorySessionExec(t, p.Ctx, peer, "SET lock_timeout=0", 0)
			for _, k := range advisorySessionKeys {
				advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", true)
			}
			advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
			advisorySessionClear(t, p.Ctx, peer)
			// Separate pools permit observing a physical close without closing
			// the observer's engine/session. Returning a borrower is not close.
			var notices []fixture.Diagnostic
			pool, borrower, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			pool.SetMaxOpenConns(1)
			pool.SetMaxIdleConns(1)
			var physical any
			if err := borrower.Raw(func(raw any) error { physical = raw; return nil }); err != nil {
				t.Fatal(err)
			}
			stmt, err := borrower.PrepareContext(p.Ctx, "SELECT pg_advisory_lock(71)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			advisorySessionValue(t, p.Ctx, fixture.HeldStatement{Executor: borrower, Statement: stmt, SQL: "SELECT pg_advisory_lock(71)"}, "SELECT pg_advisory_lock(71)", "VOID", "")
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, borrower, record, fixture.Select("SELECT true", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}))
			if err := borrower.Close(); err != nil {
				t.Fatal(err)
			}
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(71)", "BOOL", false)
			reused, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reused.Close() })
			if err := reused.Raw(func(raw any) error {
				if raw != physical {
					t.Fatal("physical pool session changed")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			advisorySessionValue(t, p.Ctx, reused, "SELECT pg_advisory_lock(71)", "VOID", "")
			var ap, bp int
			if b.PG {
				ap, bp = fixture.LockPID(t, p.Ctx, reused), fixture.LockPID(t, p.Ctx, peer)
			}
			w := advisorySessionStart(t, p.Ctx, peer, "SELECT pg_advisory_lock(71)")
			if b.PG {
				advisorySessionWait(t, p.Ctx, bp, ap, advisorySessionKey{"71", 0, 71, 1}, false)
			}
			pool.SetMaxIdleConns(0)
			if err := reused.Close(); err != nil {
				t.Fatal(err)
			}
			if err := w.await(t); err != nil {
				t.Fatal(err)
			}
			advisorySessionClear(t, p.Ctx, peer)
			fresh, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			before := len(notices)
			advisorySessionValue(t, p.Ctx, fresh, "SELECT pg_advisory_unlock(71)", "BOOL", false)
			if len(notices) != before+1 || notices[before].Code != "01000" {
				t.Fatal("fresh owner inherited prior ownership", notices)
			}
		})
	}
}

func TestReferenceAdvisorySessionFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			pool, a, _ := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			pool.SetMaxOpenConns(1)
			pool.SetMaxIdleConns(1)
			peer, third := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			stmt, err := a.PrepareContext(p.Ctx, "SELECT pg_advisory_lock($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
			exec := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: "SELECT pg_advisory_lock($1)"}
			for range 2 {
				advisorySessionValue(t, p.Ctx, exec, "SELECT pg_advisory_lock($1)", "VOID", "", int64(42))
			}
			advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
			for i := range 2 {
				advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(42)", "BOOL", false)
				advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_unlock(42)", "BOOL", true)
				if i == 1 {
					advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(42)", "BOOL", true)
				}
			}
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_advisory_unlock(42)", "BOOL", true)
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_unlock(42)", "BOOL", false)
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_lock_shared(42)", "VOID", "")
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_unlock(42)", "BOOL", false)
			wantWarning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "you don't own a lock of type ExclusiveLock"}
			if len(notices) != 2 || notices[0] != wantWarning || notices[1] != wantWarning {
				t.Fatal(notices)
			}
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_unlock_shared(42)", "BOOL", true)
			for _, c := range []*sql.Conn{a, peer} {
				advisorySessionValue(t, p.Ctx, c, "SELECT pg_advisory_lock_shared(1,2)", "VOID", "")
			}
			advisorySessionValue(t, p.Ctx, third, "SELECT pg_try_advisory_lock(1,2)", "BOOL", false)
			advisorySessionValue(t, p.Ctx, third, "SELECT pg_try_advisory_lock(4294967298::bigint)", "BOOL", true)
			advisorySessionClear(t, p.Ctx, third)
			advisorySessionClear(t, p.Ctx, peer)
			advisorySessionClear(t, p.Ctx, a)
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_lock(42)", "VOID", "")
			advisorySessionExec(t, p.Ctx, peer, "SET lock_timeout='40ms'", 0)
			advisorySessionError(t, p.Ctx, peer, "SELECT pg_advisory_lock(42)", advisorySessionTimeout("55P03", "canceling statement due to lock timeout"))
			advisorySessionExec(t, p.Ctx, peer, "SET lock_timeout=0", 0)
			var ap, bp int
			if b.PG {
				ap, bp = fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, peer)
			}
			w := advisorySessionStart(t, p.Ctx, peer, "SELECT pg_advisory_lock(42)")
			if b.PG {
				advisorySessionWait(t, p.Ctx, bp, ap, advisorySessionKey{"42", 0, 42, 1}, false)
			}
			advisorySessionClear(t, p.Ctx, a)
			if err := w.await(t); err != nil {
				t.Fatal(err)
			}
			advisorySessionClear(t, p.Ctx, peer)
			before := len(notices)
			advisorySessionClear(t, p.Ctx, a)
			if len(notices) != before {
				t.Fatal("empty unlock_all warned", notices)
			}
			if !b.PG {
				advisorySessionExec(t, p.Ctx, a, "CREATE TABLE advisory_smoke_data(id integer)", 0)
				advisorySessionExec(t, p.Ctx, a, "INSERT INTO advisory_smoke_data VALUES(7)", 1)
				for range 2 {
					advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_lock(64)", "VOID", "")
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(64)", "BOOL", false)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				injected := errors.New("smoke Restore reader failure")
				if err := stuntgres.Restore(p.Ctx, p.Native, advisorySessionFailReader{injected}); !errors.Is(err, injected) {
					t.Fatal(err)
				}
				advisorySessionValue(t, p.Ctx, a, "SELECT id FROM advisory_smoke_data", "INT4", int64(7))
				for i := range 2 {
					advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(64)", "BOOL", false)
					advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_unlock(64)", "BOOL", true)
					if i == 1 {
						advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(64)", "BOOL", true)
					}
				}
				advisorySessionClear(t, p.Ctx, peer)
			}
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_lock(99)", "VOID", "")
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(99)", "BOOL", false)
			reused, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reused.Close()
			pool.SetMaxIdleConns(0)
			if err := reused.Close(); err != nil {
				t.Fatal(err)
			}
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(99)", "BOOL", true)
			advisorySessionClear(t, p.Ctx, peer)
			advisorySessionValue(t, p.Ctx, peer, "SELECT true", "BOOL", true)
		})
	}
}
