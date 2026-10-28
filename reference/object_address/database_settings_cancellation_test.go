package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseSettingsCancellation(t *testing.T) {
	for _, kind := range []string{"cancel", "statement_timeout"} {
		t.Run(kind, func(t *testing.T) {
			dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
				a, apid := fixture.RoleTransactionConn(t, p, b)
				var notices []fixture.Diagnostic
				_, w, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				b.Recorder = recorder
				wpid := fixture.RoleTransactionPID(t, p, b, w)
				t.Cleanup(func() { w.ExecContext(p.Ctx, "ROLLBACK") })
				run := func(e fixture.Executor, s fixture.Step) { dsRun(t, p, b, e, route, s) }
				run(a, fixture.Exec("ALTER DATABASE ds_one SET application_name='committed'", 0))
				run(a, fixture.Exec("ALTER DATABASE ds_two SET application_name='peer'", 0))
				q := "ALTER DATABASE ds_one SET application_name='canceled'"
				var executor fixture.Executor = w
				if route != "direct" {
					stmt, err := w.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{Executor: w, Statement: stmt, SQL: q}
				}
				if !b.PG {
					for _, expired := range []bool{false, true} {
						ctx, cancel := context.WithCancel(p.Ctx)
						want := context.Canceled
						if expired {
							cancel()
							ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
							want = context.DeadlineExceeded
						} else {
							cancel()
						}
						r, err := executor.ExecContext(ctx, q)
						cancel()
						if r != nil || !errors.Is(err, want) {
							t.Fatalf("pre-admission result=%v error=%v want %v", r, err, want)
						}
						run(a, dsRow("ds_one", "{application_name=committed}"))
					}
				}
				run(a, fixture.Exec("BEGIN", 0))
				// The other SQL spelling owns precisely the same row lock.
				run(a, fixture.Exec("ALTER ROLE ALL IN DATABASE ds_one SET application_name='private'", 0))
				run(w, fixture.Exec("BEGIN", 0))
				if kind == "statement_timeout" {
					run(w, fixture.Exec("SET statement_timeout='100ms'", 0))
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				completed := make(chan struct{})
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(completed)
					r, err := executor.ExecContext(ctx, q)
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				// This cleanup releases the holder before LockOperation joins on failure.
				t.Cleanup(func() { a.ExecContext(p.Ctx, "ROLLBACK") })
				if kind == "cancel" {
					fixture.RoleTransactionWait(t, p, b, wpid, apid, completed)
					// Committed readers and new sessions must not join the writer queue.
					dsFresh(t, p, b, route, "ds_one", "", "committed")
					if b.PG {
						var signaled bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", wpid).Scan(&signaled); err != nil || !signaled {
							t.Fatal("cancel signal", signaled, err)
						}
					} else {
						cancel()
					}
				}
				out := join()
				if len(notices) != 0 {
					t.Fatalf("interrupted utility emitted notices: %#v", notices)
				}
				if out.Result != nil {
					t.Fatal("interrupted utility returned result")
				}
				if kind == "cancel" && !b.PG {
					if !errors.Is(out.Err, context.Canceled) {
						t.Fatal(out.Err)
					}
				} else {
					message := "canceling statement due to user request"
					if kind == "statement_timeout" {
						message = "canceling statement due to statement timeout"
					}
					want := fixture.ControlsReferenceDiagnostic("57014", message)
					if b.PG {
						// Only physical heap coordinates vary. Keep action, relation and
						// every other diagnostic field exact; oracle discovery checks this.
						d := fixture.ExtractDiagnostic(t, out.Err)
						if !regexp.MustCompile(`^while updating tuple \([0-9]+,[0-9]+\) in relation "pg_db_role_setting"$`).MatchString(d.Context) {
							t.Fatalf("unexpected settings wait context %q", d.Context)
						}
						want.Context = d.Context
					}
					fixture.AssertDiagnostic(t, out.Err, &want)
				}
				if kind == "cancel" {
					run(w, fixture.Exec("ROLLBACK", 0))
					run(w, rsDatabaseApplication(""))
				} else {
					// Dispose an armed timeout owner; no reset/probe on that owner.
					w.Raw(func(any) error { return driver.ErrBadConn })
				}
				run(a, fixture.Exec("ROLLBACK", 0))
				run(b.DB, dsRow("ds_one", "{application_name=committed}"))
				run(b.DB, dsRow("ds_two", "{application_name=peer}"))
				dsFresh(t, p, b, route, "ds_one", "", "committed")
				run(b.DB, fixture.Exec("ALTER DATABASE ds_one SET application_name='after'", 0))
				dsFresh(t, p, b, route, "ds_one", "", "after")
			})
		})
	}
}
