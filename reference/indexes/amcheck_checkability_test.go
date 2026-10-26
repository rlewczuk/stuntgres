package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Numeric regclass inputs keep binder name lookup out of the lock/checkability
// precedence witness. REL_18_6 verify_common.c locks heap then index before
// index_checkable; indexam.c's index_open locks a non-index target before 42809.
func TestReferenceAmcheckCheckability(t *testing.T) {
	for _, target := range []string{"am_phase_hash", "am_phase_heap"} {
		for _, function := range []string{"bt_index_check", "bt_index_parent_check"} {
			for _, route := range []string{"direct", "held"} {
				for _, outcome := range []string{"release", "cancel", "lock_timeout", "statement_timeout"} {
					t.Run(target+"/"+function+"/"+route+"/"+outcome, func(t *testing.T) {
						p := fixture.NewScenario(t)
						p.Step(t, fixture.Exec("CREATE EXTENSION amcheck", 0))
						p.Step(t, fixture.Exec("CREATE TABLE am_phase_heap(n INT)", 0))
						p.Step(t, fixture.Exec("CREATE INDEX am_phase_hash ON am_phase_heap USING hash(n)", 0))
						p.Step(t, fixture.Exec("CREATE INDEX am_phase_btree ON am_phase_heap(n)", 0))
						p.Step(t, fixture.Exec("INSERT INTO am_phase_heap VALUES(1),(2)", 2))
						for _, backend := range p.Backends() {
							t.Run(backend.Name, func(t *testing.T) {
								amcheckCheckabilityAdmission(t, p, backend, target, function, route, outcome)
							})
						}
					})
				}
			}
		}
	}
}

func amcheckCheckabilityAdmission(t *testing.T, p *fixture.Pair, backend fixture.Backend, target, function, route, outcome string) {
	t.Helper()
	a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
	var oid int64
	if err := b.QueryRowContext(p.Ctx, "SELECT '"+target+"'::regclass::oid").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if outcome == "lock_timeout" || outcome == "statement_timeout" {
		t.Cleanup(func() {
			if err := b.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Error("retiring armed owner", err)
			}
		})
	}
	query := fmt.Sprintf("SELECT %s(%d::regclass)", function, oid)
	var executor fixture.Executor = b
	if route == "held" {
		statement, err := b.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := statement.Close(); err != nil {
				t.Error(err)
			}
		})
		executor = fixture.HeldStatement{Executor: b, Statement: statement, SQL: query}
	}
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: `expected "btree" index as targets for verification`, Detail: `Relation "am_phase_hash" is a hash index.`}
	if target == "am_phase_heap" {
		want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"am_phase_heap" is not an index`}
	}
	holder, waiter := 0, 0
	var relation int64
	if backend.PG {
		holder = fixture.LockPID(t, p.Ctx, a)
		waiter = fixture.LockPID(t, p.Ctx, b)
		relation = fixture.LockOID(t, p.Ctx, "am_phase_heap")
	}
	tx, err := a.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE am_phase_heap IN ACCESS EXCLUSIVE MODE", 0)
	call := func(ctx context.Context) fixture.RelationOutcome {
		result, err := executor.ExecContext(ctx, query)
		return fixture.RelationOutcome{Result: result, Err: err}
	}
	switch outcome {
	case "release":
		finished := make(chan struct{})
		join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(finished)
			return call(ctx)
		})
		mode := "AccessShareLock"
		if function == "bt_index_parent_check" {
			mode = "ShareLock"
		}
		fixture.RelationLockPending(t, p, backend, waiter, holder, relation, mode, finished)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		got := join()
		if got.Result != nil || got.Rows != nil {
			t.Fatal("rejected checker published output")
		}
		fixture.AssertDiagnostic(t, got.Err, &want)
	case "cancel":
		// This observes an actual relation wait before native context/server-side
		// PostgreSQL cancellation, then checks the complete cancellation outcome.
		fixture.RelationLockActiveCancel(t, p, backend, b, query, call)
	default:
		// Prepare and all setup precede arming. Never execute RESET on this
		// physical owner, even after the expected timeout has been delivered.
		interval, code, message := "10ms", "55P03", "canceling statement due to lock timeout"
		if outcome == "statement_timeout" {
			interval, code, message = "100ms", "57014", "canceling statement due to statement timeout"
		}
		fixture.LockExec(t, p.Ctx, b, "SET "+outcome+"='"+interval+"'", 0)
		failure := fixture.ImplicitError(query, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message})
		if route == "held" {
			failure.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, executor, backend.Recorder, failure)
	}
	if outcome != "release" {
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	// A fresh owner can acquire AX immediately: neither rejection, cancellation
	// nor timeout leaves a scoped heap/index contribution behind. It also proves
	// the original table contents and the healthy checker remain usable.
	fresh, err := backend.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	cleanup, err := fresh.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup.Rollback()
	fixture.LockExec(t, p.Ctx, cleanup, "LOCK TABLE am_phase_heap IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	if err := cleanup.Rollback(); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, fresh, backend.Recorder, fixture.Select("SELECT n FROM am_phase_heap ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	fixture.RunStep(t, p.Ctx, fresh, backend.Recorder, amcheckSelect("SELECT bt_index_parent_check('am_phase_btree',true,true,true) AS ok", false))
	if outcome == "cancel" || outcome == "release" {
		// The admitted held artifact remains executable; it still rejects the
		// same original numeric target after the blocker has gone.
		failure := fixture.ImplicitError(query, want)
		if route == "held" {
			failure.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, executor, backend.Recorder, failure)
		fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT 1 AS ok", []string{"ok"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
	// DROP also needs the index relation's exclusive lock, so a leaked index
	// contribution cannot hide behind the table-only NOWAIT witness above.
	drop := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		result, err := fresh.ExecContext(ctx, "DROP INDEX am_phase_hash")
		return fixture.RelationOutcome{Result: result, Err: err}
	})
	dropped := drop()
	if dropped.Err != nil {
		t.Fatal(dropped.Err)
	}
	fixture.LockAffected(t, dropped.Result, 0)
}
