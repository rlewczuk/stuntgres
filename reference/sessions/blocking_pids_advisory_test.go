package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func blockingAdvisoryRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"pg_advisory_lock"}) {
		t.Fatal("advisory labels", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "VOID" {
		t.Fatal("advisory metadata", types, err)
	}
	if !rows.Next() {
		t.Fatal("missing advisory result", rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if fixture.Normalize(raw) != "" {
		t.Fatalf("advisory VOID result %#v", raw)
	}
	if rows.Next() {
		t.Fatal("extra advisory result")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceBlockingPidsAdvisory(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					holder, hid := blockingConn(t, p, b)
					waiter, wid := blockingConn(t, p, b)
					observer, _ := blockingConn(t, p, b)
					const acquire = "SELECT pg_advisory_lock(187418::bigint)"
					const unlock = "SELECT pg_advisory_unlock(187418::bigint)"
					const try = "SELECT pg_try_advisory_lock(187418::bigint)"
					value := func(c fixture.Executor, q, label, typ string, want any) {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{label}, []string{typ}, [][]any{{want}}))
					}
					value(holder, acquire, "pg_advisory_lock", "VOID", "")
					var consumer fixture.Executor = waiter
					if prepared {
						s, err := waiter.PrepareContext(p.Ctx, acquire)
						if err != nil {
							t.Fatal(err)
						}
						// Worker cleanup is registered later and must join before Close.
						t.Cleanup(func() { _ = s.Close() })
						consumer = fixture.HeldStatement{Executor: waiter, Statement: s, SQL: acquire}
					}
					namespaceExec(t, p, b, waiter, "BEGIN")
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					awaitCanceled := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						rows, err := consumer.QueryContext(ctx, acquire)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					blockingWait(t, p, b, observer, "pg_blocking_pids", wid, hid)
					blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", wid)
					if b.PG {
						var canceled bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", wid).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
					} else {
						cancel()
					}
					o := awaitCanceled()
					if o.Rows != nil {
						o.Rows.Close()
						t.Fatal("canceled advisory wait exposed rows")
					}
					if b.PG {
						fixture.WaitsDiagnostic(t, o.Err, "57014", "canceling statement due to user request")
					} else if !errors.Is(o.Err, context.Canceled) {
						t.Fatal("ordinary caller cancellation", o.Err)
					}
					blockingWant(t, p, b, observer, "pg_blocking_pids", wid)
					blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", wid)
					s := fixture.ErrorDiagnostic("SELECT true", fixture.TransactionAborted())
					s.SelectRows = true
					fixture.RunStep(t, p.Ctx, waiter, b.Recorder, s)
					namespaceExec(t, p, b, waiter, "ROLLBACK")
					value(observer, try, "pg_try_advisory_lock", "BOOL", false)
					// New transaction provenance on the same physical waiter must
					// expose the same holder, without resurrecting its canceled request.
					namespaceExec(t, p, b, waiter, "BEGIN")
					awaitTransferred := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						rows, err := consumer.QueryContext(ctx, acquire)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					blockingWait(t, p, b, observer, "pg_blocking_pids", wid, hid)
					blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", wid)
					value(holder, unlock, "pg_advisory_unlock", "BOOL", true)
					o = awaitTransferred()
					if o.Err != nil || o.Rows == nil {
						t.Fatal("advisory transfer", o.Err)
					}
					blockingAdvisoryRows(t, o.Rows)
					blockingWant(t, p, b, observer, "pg_blocking_pids", wid)
					blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", wid)
					namespaceExec(t, p, b, waiter, "COMMIT")
					value(observer, try, "pg_try_advisory_lock", "BOOL", false)
					// Exactly one successful acquisition needs exactly one unlock.
					// A canceled-attempt count leak would keep the observer blocked.
					value(waiter, unlock, "pg_advisory_unlock", "BOOL", true)
					value(observer, try, "pg_try_advisory_lock", "BOOL", true)
					value(observer, unlock, "pg_advisory_unlock", "BOOL", true)
				})
			}
		})
	}
}
