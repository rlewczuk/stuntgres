package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationLockWaitsNowait(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx := p.Ctx
					a, b := fixture.LockConnections(t, ctx, backend.DB)
					fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					fixture.LockExec(t, ctx, a, `CREATE TABLE "T"(v INT)`, 0)
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					fixture.LockExec(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					fixture.LockExec(t, ctx, holder, `LOCK TABLE "T" IN ACCESS EXCLUSIVE MODE`, 0)
					fixture.LockExec(t, ctx, holder, "INSERT INTO t VALUES(1)", 1)
					for _, name := range []string{"t", "public.t", `public."t"`, `"T"`, `public."T"`} {
						waiter, err := b.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						q := "LOCK TABLE " + name + " IN ACCESS EXCLUSIVE MODE NOWAIT"
						if prepared {
							stmt, e := waiter.PrepareContext(ctx, q)
							if e != nil {
								waiter.Rollback()
								t.Fatal("metadata-only Prepare", e)
							}
							_, err = stmt.ExecContext(ctx)
							stmt.Close()
						} else {
							_, err = waiter.ExecContext(ctx, q)
						}
						decoded := name
						if name == `public."t"` {
							decoded = "public.t"
						}
						if name == `"T"` {
							decoded = "T"
						}
						if name == `public."T"` {
							decoded = "public.T"
						}
						fixture.WaitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "`+decoded+`"`)
						_, err = waiter.ExecContext(ctx, "SELECT v FROM t")
						fixture.WaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
						if err = waiter.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					if err = holder.Commit(); err != nil {
						t.Fatal(err)
					}
					free, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer free.Rollback()
					for range 2 {
						fixture.LockExec(t, ctx, free, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
					}
					rows, err := free.QueryContext(ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 1)
				})
			}
		})
	}
}

func TestReferenceRelationLockWaitsTimeout(t *testing.T) {
	for _, phase := range []string{"direct", "prepare", "cached"} {
		// Prove admission and same-owner recovery without SQL timers first.
		// The short timed probe then owns a disposable physical connection.
		for _, tc := range []struct{ lock, statement, code, message string }{
			{"100ms", "0", "55P03", "canceling statement due to lock timeout"},
			{"100ms", "1s", "55P03", "canceling statement due to lock timeout"},
			{"1s", "100ms", "57014", "canceling statement due to statement timeout"},
			{"100ms", "100ms", "57014", "canceling statement due to statement timeout"},
		} {
			t.Run(phase+"/"+tc.lock+"/"+tc.statement, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						ctx := p.Ctx
						a, b := fixture.LockConnections(t, ctx, backend.DB)
						t.Cleanup(func() {
							if err := b.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
								t.Errorf("retire timed physical owner: %v", err)
							}
						})
						fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						fixture.LockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
						physical := fixture.SettingsPhysical(t, b)
						ap, bp := 0, 0
						var oid int64
						if backend.PG {
							ap = fixture.LockPID(t, ctx, a)
							bp = fixture.LockPID(t, ctx, b)
							if ap == bp {
								t.Fatal("holder and waiter share a physical owner")
							}
							oid = fixture.LockOID(t, ctx, "public.t")
						}
						var cached *sql.Stmt
						if phase == "cached" {
							var err error
							cached, err = b.PrepareContext(ctx, "SELECT v FROM t")
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() {
								if err := cached.Close(); err != nil {
									t.Error("close cached statement", err)
								}
							})
						}
						operation := func(ctx context.Context) fixture.RelationOutcome {
							switch phase {
							case "prepare":
								stmt, err := b.PrepareContext(ctx, "SELECT v FROM t")
								return fixture.RelationOutcome{Stmt: stmt, Err: err}
							case "cached":
								rows, err := cached.QueryContext(ctx)
								return fixture.RelationOutcome{Rows: rows, Err: err}
							default:
								rows, err := b.QueryContext(ctx, "SELECT v FROM t")
								return fixture.RelationOutcome{Rows: rows, Err: err}
							}
						}
						noHandles := func(o fixture.RelationOutcome) {
							t.Helper()
							if o.Rows != nil {
								if err := o.Rows.Close(); err != nil {
									t.Error("close unexpected rows", err)
								}
							}
							if o.Stmt != nil {
								if err := o.Stmt.Close(); err != nil {
									t.Error("close unexpected statement", err)
								}
							}
							if o.Rows != nil || o.Stmt != nil || o.Result != nil {
								t.Fatal("failed operation published a handle or result")
							}
						}
						newHolder := func(nowait bool) *sql.Tx {
							t.Helper()
							holder, err := a.BeginTx(ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() {
								if err := holder.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
									t.Error("cleanup holder", err)
								}
							})
							query := "LOCK TABLE t IN ACCESS EXCLUSIVE MODE"
							if nowait {
								query += " NOWAIT"
							}
							fixture.LockExec(t, ctx, holder, query, 0)
							return holder
						}

						// Cancel an admitted implicit command on the original owner at
						// timeout zero. Server cancellation preserves pq's transport.
						control := newHolder(false)
						fixture.LockExec(t, ctx, control, "INSERT INTO t VALUES(1)", 1)
						cancelCtx, cancel := context.WithCancel(ctx)
						t.Cleanup(cancel)
						finished := make(chan struct{})
						awaitControl := fixture.LockOperation(t, cancelCtx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							return operation(ctx)
						})
						fixture.RelationLockPending(t, p, backend, bp, ap, oid, "AccessShareLock", finished)
						if backend.PG {
							var signaled bool
							if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&signaled); err != nil || !signaled {
								t.Fatal("cancel admitted backend", signaled, err)
							}
						} else {
							cancel()
						}
						controlOutcome := awaitControl()
						noHandles(controlOutcome)
						cancel()
						if backend.PG {
							fixture.WaitsDiagnostic(t, controlOutcome.Err, "57014", "canceling statement due to user request")
						} else {
							if !errors.Is(controlOutcome.Err, context.Canceled) {
								t.Fatalf("native admitted cancellation: %v", controlOutcome.Err)
							}
							var diagnostic interface{ SQLState() string }
							if errors.As(controlOutcome.Err, &diagnostic) {
								t.Fatalf("native context cancellation has SQLSTATE %q", diagnostic.SQLState())
							}
						}
						if err := control.Rollback(); err != nil {
							t.Fatal(err)
						}
						if fixture.SettingsPhysical(t, b) != physical {
							t.Fatal("cancellation replaced the original physical owner")
						}
						if backend.PG && fixture.LockPID(t, ctx, b) != bp {
							t.Fatal("cancellation replaced the original backend")
						}
						rows, err := b.QueryContext(ctx, "SELECT v FROM t")
						if err != nil {
							t.Fatal(err)
						}
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 7)

						// NOWAIT detects cancellation leakage while b is still alive.
						holder := newHolder(true)
						fixture.LockExec(t, ctx, holder, "INSERT INTO t VALUES(1)", 1)
						fixture.LockExec(t, ctx, b, "SET lock_timeout='"+tc.lock+"'", 0)
						fixture.LockExec(t, ctx, b, "SET statement_timeout='"+tc.statement+"'", 0)
						// No SQL on b after arming, and no short-timer observer race.
						await := fixture.LockOperation(t, ctx, operation)
						o := await()
						noHandles(o)
						position := ""
						if tc.code == "55P03" && phase != "cached" {
							position = "15"
						}
						fixture.WaitsDiagnostic(t, o.Err, tc.code, tc.message, position)
						if err = holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						// Prove provisional locks are gone before owner retirement can
						// hide a leak. All subsequent SQL belongs to healthy observer a.
						probe := newHolder(true)
						if err := probe.Rollback(); err != nil {
							t.Fatal(err)
						}
						rows, err = a.QueryContext(ctx, "SELECT v FROM t")
						if err != nil {
							t.Fatal(err)
						}
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 7)
						fixture.LockExec(t, ctx, a, "INSERT INTO t VALUES(2)", 1)
						rows, err = a.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 2, 7)
					})
				}
			})
		}
	}
}

func TestReferenceRelationLockWaitsSettings(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			conn, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			check := func(want string) {
				t.Helper()
				var got string
				if err := conn.QueryRowContext(ctx, "SHOW lock_timeout").Scan(&got); err != nil || got != want {
					t.Fatalf("SHOW=%q (%v), want %q", got, err, want)
				}
			}
			check("0")
			for _, tc := range []struct{ input, want string }{{"1000", "1s"}, {"1500ms", "1500ms"}, {"1.5s", "1500ms"}, {"1000us", "1ms"}, {"0.5ms", "0"}} {
				fixture.LockExec(t, ctx, conn, "SET lock_timeout='"+tc.input+"'", 0)
				check(tc.want)
			}
			for _, tc := range []struct{ input, message, hint string }{
				{"-1", `-1 ms is outside the valid range for parameter "lock_timeout" (0 ms .. 2147483647 ms)`, ""},
				{"1 MS", `invalid value for parameter "lock_timeout": "1 MS"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`},
				{"2147483648", `invalid value for parameter "lock_timeout": "2147483648"`, "Value exceeds integer range."},
			} {
				_, err = conn.ExecContext(ctx, "SET lock_timeout='"+tc.input+"'")
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: tc.message, Hint: tc.hint}
				if got := fixture.ExtractDiagnostic(t, err); got != want {
					t.Fatal(got, want)
				}
				fixture.State(t, err, "22023")
				check("0")
			}
			for _, commit := range []bool{false, true} {
				fixture.LockExec(t, ctx, conn, "SET lock_timeout='1s'", 0)
				tx, err := conn.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				fixture.LockExec(t, ctx, tx, "SET LOCAL lock_timeout='2s'", 0)
				var got string
				if err = tx.QueryRowContext(ctx, "SHOW lock_timeout").Scan(&got); err != nil || got != "2s" {
					tx.Rollback()
					t.Fatal(got, err)
				}
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				check("1s")
			}
			fixture.LockExec(t, ctx, conn, "RESET lock_timeout", 0)
			check("0")
			var value string
			if err = conn.QueryRowContext(ctx, "SELECT set_config('lock_timeout','1500ms',false)").Scan(&value); err != nil || value != "1500ms" {
				t.Fatal(value, err)
			}
			if err = conn.QueryRowContext(ctx, "SELECT current_setting('lock_timeout')").Scan(&value); err != nil || value != "1500ms" {
				t.Fatal(value, err)
			}
			fixture.LockExec(t, ctx, conn, "SET SESSION lock_timeout=DEFAULT", 0)
			check("0")
			fixture.LockExec(t, ctx, conn, "SET lock_timeout='1s'", 0)
			fixture.LockExec(t, ctx, conn, "RESET ALL", 0)
			check("0")
		})
	}
}
