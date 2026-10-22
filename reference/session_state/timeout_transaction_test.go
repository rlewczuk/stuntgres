package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimeoutTransactionBoundaries(t *testing.T) {
	for _, block := range []string{"implicit", "sql", "go"} {
		for _, phase := range []string{"direct", "prepare", "held"} {
			t.Run(block+"/"+phase, func(t *testing.T) {
				timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					f := newTimeoutReferenceFixture(t, p, b)
					var held *sql.Stmt
					var tx *sql.Tx
					var executor fixture.Executor = f.owner
					if phase == "held" && block != "go" {
						var err error
						held, err = f.owner.PrepareContext(p.Ctx, "SELECT v FROM timeout_blocked")
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { held.Close() })
					}
					f.set("transaction_timeout", "250ms")
					if block == "sql" {
						f.beginPrivate()
					}
					if block == "go" {
						var err error
						tx, err = f.owner.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { tx.Rollback() })
						executor = tx
						f.exec(tx, "LOCK TABLE timeout_sentinel IN ACCESS EXCLUSIVE MODE", 0)
						f.exec(tx, "INSERT INTO timeout_sentinel VALUES(8)", 1)
						if phase == "held" {
							// LOCK Prepare is metadata-only: unlike SELECT Prepare
							// it retains no AS before the peer acquires AX.
							held, err = tx.PrepareContext(p.Ctx, "LOCK TABLE timeout_blocked IN ACCESS SHARE MODE")
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { held.Close() })
						}
					}
					release := f.holdBlocked()
					operationPhase := phase
					query := "SELECT v FROM timeout_blocked"
					if block == "go" && phase == "held" {
						operationPhase = "held-lock-share"
						query = "LOCK TABLE timeout_blocked IN ACCESS SHARE MODE"
					}
					o := f.blocked(operationPhase, query, held, executor)
					position := "15"
					if phase == "held" {
						position = ""
					}
					diagnosticPhase := phase
					if block == "go" && phase == "held" {
						diagnosticPhase = "tx-held"
					}
					f.diagnostic(o.Err, "25P04", diagnosticPhase, position)
					if tx != nil {
						_ = tx.Rollback()
					}
					f.dead()
					release()
					f.rows(f.peer, 7)
				})
			})
		}
	}
	for _, stage := range []string{"direct", "prepare", "prepared-execute"} {
		t.Run("cpu/"+stage, func(t *testing.T) {
			timedOut := make(map[string]bool)
			for _, size := range []int{16384, 65536, 262144, 1048576} {
				t.Run(fmt.Sprint(size), func(t *testing.T) {
					timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						if done, seen := timedOut[b.Name]; seen && done {
							return
						}
						timedOut[b.Name] = false
						f := newTimeoutReferenceFixture(t, p, b)
						query := settingsTimeoutWorkload("timeout_sentinel", size)
						var held *sql.Stmt
						if stage == "prepared-execute" {
							var err error
							held, err = f.owner.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { held.Close() })
						}
						f.set("transaction_timeout", "1ms")
						var err error
						switch stage {
						case "prepare":
							var stmt *sql.Stmt
							stmt, err = f.owner.PrepareContext(p.Ctx, query)
							if stmt != nil {
								stmt.Close()
								if err != nil {
									t.Fatal("failed Prepare returned handle")
								}
							}
						case "prepared-execute":
							_, err = held.ExecContext(p.Ctx)
						default:
							_, err = f.owner.ExecContext(p.Ctx, query)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) && state.SQLState() == "25P04" ||
							b.PG && (errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone)) {
							phase := "direct"
							if stage == "prepared-execute" {
								phase = "held"
							}
							position := ""
							// Pinned PG reaches semantic analysis (Position13); native
							// CPU Prepare cancels its incomplete full parse, location-free.
							// Deterministic locked semantic Prepare still asserts15.
							if stage == "prepare" && b.PG {
								position = "13"
							}
							f.diagnostic(err, "25P04", phase, position)
							f.dead()
							timedOut[b.Name] = true
						} else if stage == "prepare" && err == nil {
							// A completed metadata command is not a running transaction.
						} else {
							want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "timeout_sentinel" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "timeout_sentinel", Column: "v"}
							fixture.AssertDiagnostic(t, err, &want)
						}
						f.rows(f.peer, 7)
					})
				})
				allTimedOut := len(timedOut) != 0
				for _, done := range timedOut {
					allTimedOut = allTimedOut && done
				}
				if allTimedOut {
					break
				}
			}
			if len(timedOut) == 0 {
				t.Fatal("timeout scenario executed no backend")
			}
			for backend, done := range timedOut {
				if !done {
					t.Fatalf("%s bounded actual CPU workload did not fire transaction timeout (O4)", backend)
				}
			}
		})
	}
	t.Run("completion-and-detached-rows", func(t *testing.T) {
		timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			f := newTimeoutReferenceFixture(t, p, b)
			f.set("transaction_timeout", "250ms")
			rows, err := f.owner.QueryContext(p.Ctx, "SELECT v FROM timeout_sentinel")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			check := settingsTimeoutCursor(t, rows, b.PG)
			timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
			f.show("transaction_timeout", "250ms")
			if !b.PG {
				check()
			}
			f.exec(f.owner, "BEGIN", 0)
			f.exec(f.owner, "INSERT INTO timeout_sentinel VALUES(8)", 1)
			f.exec(f.owner, "COMMIT", 0)
			timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
			f.rows(f.owner, 7, 8)
		})
	})
}

func TestReferenceTimeoutTransactionAssignments(t *testing.T) {
	for _, kind := range []string{"commands", "positive-positive", "same-value", "disable", "reenable", "reset", "reset-all", "local-rollback", "local-commit", "session-commit", "savepoint-restore", "set-config"} {
		t.Run(kind, func(t *testing.T) {
			timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				f := newTimeoutReferenceFixture(t, p, b)
				identity := fixture.SettingsPhysical(t, f.owner)
				phase, before, after := kind, time.Now(), time.Time{}
				mark := func(query string) { phase, before, after = query, time.Now(), time.Time{} }
				t.Cleanup(func() {
					if t.Failed() {
						t.Logf("assignment=%s phase/query=%s before=%v after=%v elapsed=%v", kind, phase, before, after, time.Since(before))
					}
				})
				run := func(s fixture.Step) {
					mark(s.Query)
					fixture.RunStep(t, p.Ctx, f.owner, b.Recorder, s)
					after = time.Now()
				}
				exec := func(q string) { run(fixture.Exec(q, 0)) }
				set := func(v string) { exec("SET transaction_timeout='" + v + "'") }
				show := func(v string) { run(localReferenceSelect(b.PG, "SHOW transaction_timeout", "transaction_timeout", v)) }
				idle := func(d time.Duration) {
					mark("idle (no owner SQL)")
					timeoutReferenceIdle(t, p.Ctx, d)
					after = time.Now()
				}
				if kind == "positive-positive" || kind == "same-value" || kind == "commands" {
					mark("positiveAssignment/" + kind)
					f.positiveAssignment(kind)
					return
				}
				if kind == "local-rollback" || kind == "local-commit" || kind == "session-commit" {
					exec("BEGIN")
					mode := "LOCAL"
					if kind == "session-commit" {
						mode = "SESSION"
					}
					exec("SET " + mode + " transaction_timeout='250ms'")
					end := "COMMIT"
					if kind == "local-rollback" {
						end = "ROLLBACK"
					}
					exec(end)
					idle(350 * time.Millisecond)
					want := "0"
					if kind == "session-commit" {
						want = "250ms"
					}
					show(want)
					if fixture.SettingsPhysical(t, f.owner) != identity {
						t.Fatal("completed transaction lost physical owner")
					}
					f.rows(f.peer, 7)
					return
				}
				if kind == "savepoint-restore" {
					identity = f.prepareSavepointRestore(mark)
				} else {
					set("250ms")
					exec("BEGIN")
				}
				switch kind {
				case "disable", "reset", "reset-all":
					switch kind {
					case "disable":
						set("0")
					case "reset":
						exec("RESET transaction_timeout")
					case "reset-all":
						exec("RESET ALL")
					}
					idle(350 * time.Millisecond)
					show("0")
					exec("ROLLBACK")
					show("250ms")
					if fixture.SettingsPhysical(t, f.owner) != identity {
						t.Fatal("reset/disabled timeout lost physical owner")
					}
					mark("peer SELECT v FROM timeout_sentinel ORDER BY v")
					f.rows(f.peer, 7)
					return
				case "reenable", "savepoint-restore", "set-config":
					if kind == "set-config" {
						run(localReferenceSelect(b.PG, "SELECT set_config('transaction_timeout','0',false)", "set_config", "0"))
					} else if kind != "savepoint-restore" {
						set("0")
					}
					idle(350 * time.Millisecond)
					show("0")
					if fixture.SettingsPhysical(t, f.owner) != identity {
						t.Fatal("retired timeout lost physical owner")
					}
					if kind == "savepoint-restore" {
						exec("ROLLBACK TO timeout_restore")
					} else {
						set("250ms")
					}
					idle(50 * time.Millisecond)
					show("250ms")
				}
				if kind == "savepoint-restore" {
					idle(350 * time.Millisecond)
					// Restoring SHOW250 after SET0 does not
					// rearm the retired budget. A real later off/on still does.
					show("250ms")
					mark("SELECT v FROM timeout_sentinel ORDER BY v")
					f.rows(f.owner, 7)
					if fixture.SettingsPhysical(t, f.owner) != identity {
						t.Fatal("restored timeout lost physical owner")
					}
					set("0")
					set("250ms")
				}
				mark("await transaction FATAL25P04 / first fatal SQL")
				f.awaitTransactionFatal(p.Ctx)
				mark("dead owner INSERT99 must fail without output")
				f.dead()
				mark("peer SELECT v FROM timeout_sentinel ORDER BY v")
				f.rows(f.peer, 7)
			})
		})
	}
}

// Only the short, still-armed preparation may adapt to a legitimate expiry.
// SET0 commits us to the semantic witness: nothing after it is retried.
func (f *timeoutReferenceFixture) prepareSavepointRestore(mark func(string)) any {
	t := f.t
	t.Helper()
	start := time.Now()
	parentEnd, ok := f.p.Ctx.Deadline()
	if !ok || time.Until(parentEnd) <= 4*time.Second {
		t.Fatal("savepoint preparation lacks four-second semantic reserve")
	}
	end := start.Add(6 * time.Second)
	if reserve := parentEnd.Add(-4 * time.Second); reserve.Before(end) {
		end = reserve
	}
	ctx, cancel := context.WithDeadline(f.p.Ctx, end)
	defer cancel()
	// Registered before arming; even an assertion on a live attempt must not
	// return an armed transaction to the pool.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(f.p.Ctx, time.Second)
		defer cancel()
		_, _ = f.owner.ExecContext(ctx, "ROLLBACK")
		_ = f.owner.Raw(func(any) error { return driver.ErrBadConn })
		_ = f.owner.Close()
	})
	for attempt := 1; attempt <= 3; attempt++ {
		f.requireAttemptTime(250 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatal("savepoint preparation allowance expired", ctx.Err())
		}
		identity := fixture.SettingsPhysical(t, f.owner)
		var beginBefore, beginAfter time.Time
		expired := false
		for _, q := range []string{"SET transaction_timeout='250ms'", "BEGIN", "SAVEPOINT timeout_restore", "SET transaction_timeout='0'"} {
			mark(fmt.Sprintf("savepoint preparation attempt=%d query=%s", attempt, q))
			before := time.Now()
			result, err := f.owner.ExecContext(ctx, q)
			after := time.Now()
			if q == "BEGIN" {
				beginBefore, beginAfter = before, after
			}
			earliest := beginBefore.Add(250 * time.Millisecond)
			if q == "SET transaction_timeout='250ms'" {
				earliest = before.Add(250 * time.Millisecond)
			}
			if err != nil {
				t.Logf("savepoint preparation attempt=%d query=%s command=[%v,%v] BEGIN=[%v,%v] earliest=%v elapsed=%v error=%v", attempt, q, before, after, beginBefore, beginAfter, earliest, after.Sub(start), err)
				if result != nil {
					t.Fatal("failed savepoint preparation published result", result)
				}
				if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("savepoint preparation context failure, not configured expiry", ctx.Err(), err)
				}
				if after.Before(earliest) {
					t.Fatal("savepoint preparation failed before configured expiry", err)
				}
				f.preparationExpired(err, earliest)
				f.rows(f.peer, 7)
				_ = f.owner.Raw(func(any) error { return driver.ErrBadConn })
				_ = f.owner.Close()
				expired = true
				break
			}
			if result == nil {
				t.Fatal("savepoint preparation omitted result", q)
			}
			if n, err := result.RowsAffected(); err != nil || n != 0 {
				t.Fatal("savepoint preparation affected count", q, n, err)
			}
			if ctx.Err() != nil {
				t.Fatal("savepoint preparation allowance expired", ctx.Err())
			}
			if fixture.SettingsPhysical(t, f.owner) != identity {
				t.Fatal("savepoint preparation changed physical owner", q)
			}
		}
		if !expired {
			return identity
		}
		if attempt == 3 {
			t.Fatal("savepoint preparation exhausted three fixed250ms attempts")
		}
		if ctx.Err() != nil {
			t.Fatal("savepoint preparation allowance expired after disposal", ctx.Err())
		}
		f.replaceTimeoutOwner()
	}
	panic("unreachable savepoint preparation")
}

func (f timeoutReferenceFixture) positiveAssignment(kind string) {
	t := f.t
	t.Helper()
	for budget := 200 * time.Millisecond; budget <= 6400*time.Millisecond; budget *= 2 {
		f.requireAttemptTime(budget)
		identity := fixture.SettingsPhysical(t, f.owner)
		value := fmt.Sprintf("%dms", budget.Milliseconds())
		assignedBudget := budget
		if kind == "positive-positive" {
			assignedBudget = 2 * budget
		}
		assigned := fmt.Sprintf("%dms", assignedBudget.Milliseconds())
		f.set("transaction_timeout", value)
		beginBefore := time.Now()
		err := f.preparationExec("BEGIN", 0)
		beginAfter := time.Now()
		if err != nil {
			f.preparationExpired(err, beginBefore.Add(budget))
			f.rows(f.peer, 7)
			f.replaceTimeoutOwner()
			continue
		}
		// A positive assignment must occur late enough to distinguish an
		// unchanged BEGIN deadline from an incorrectly refreshed one.
		timeoutReferenceUntil(t, f.p.Ctx, beginAfter.Add(budget/2))
		assignmentBefore := time.Now()
		if !assignmentBefore.Before(beginBefore.Add(budget)) {
			f.awaitTransactionFatal(f.p.Ctx)
			f.dead()
			f.rows(f.peer, 7)
			f.replaceTimeoutOwner()
			continue
		}
		if kind != "commands" {
			err = f.preparationExec("SET transaction_timeout='"+assigned+"'", 0)
		}
		if err == nil {
			err = f.preparationShow(assigned)
		}
		if err == nil && kind == "commands" {
			err = f.preparationShow(assigned)
		}
		if err != nil {
			f.preparationExpired(err, beginBefore.Add(budget))
			f.rows(f.peer, 7)
			f.replaceTimeoutOwner()
			continue
		}
		if fixture.SettingsPhysical(t, f.owner) != identity {
			t.Fatal("timeout assignment lost physical owner")
		}
		assignmentAfter := time.Now()
		if !assignmentAfter.Before(beginBefore.Add(budget)) {
			f.awaitTransactionFatal(f.p.Ctx)
			f.dead()
			f.rows(f.peer, 7)
			f.replaceTimeoutOwner()
			continue
		}
		// Preparation is complete. Assertion failures below are not retried.
		decisive := f.originalExpiry(beginAfter.Add(budget),
			assignmentBefore.Add(assignedBudget), assignmentAfter.Add(assignedBudget))
		f.rows(f.peer, 7)
		if decisive {
			return
		}
		f.replaceTimeoutOwner()
	}
	t.Fatal("adaptive transaction assignments produced no decisive deadline witness")
}

func TestReferenceTimeoutTransactionAbortRestore(t *testing.T) {
	for _, failure := range []string{"semantic", "statement", "lock"} {
		for _, child := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/child=%v", failure, child), func(t *testing.T) {
				timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					f := newTimeoutReferenceFixture(t, p, b)
					identity := fixture.SettingsPhysical(t, f.owner)
					f.set("transaction_timeout", "500ms")
					// Configured 1s idle limit is suppressed even after ERROR stops
					// the actual transaction timer (release ReadyForQuery rule).
					f.set("idle_in_transaction_session_timeout", "1s")
					if failure == "statement" {
						f.set("statement_timeout", "125ms")
					}
					if failure == "lock" {
						f.set("lock_timeout", "125ms")
					}
					f.beginPrivate()
					if child {
						f.exec(f.owner, "SAVEPOINT timeout_child", 0)
					}
					if failure == "semantic" {
						_, err := f.owner.ExecContext(p.Ctx, "INSERT INTO timeout_sentinel VALUES(NULL)")
						want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "timeout_sentinel" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "timeout_sentinel", Column: "v"}
						fixture.AssertDiagnostic(t, err, &want)
					} else {
						release := f.holdBlocked()
						o := f.blocked("utility", "LOCK TABLE timeout_blocked IN ACCESS EXCLUSIVE MODE", nil)
						code := "57014"
						if failure == "lock" {
							code = "55P03"
						}
						timeoutReferenceDiagnostic(t, o.Err, code)
						release()
					}
					// O3 source hypothesis: full abort cancels all timers. An
					// unchanged positive restoration must not invent a new timer.
					timeoutReferenceIdle(t, p.Ctx, 1100*time.Millisecond)
					_, err := f.owner.ExecContext(p.Ctx, "SELECT v FROM timeout_sentinel")
					want := fixture.TransactionAborted()
					fixture.AssertDiagnostic(t, err, &want)
					if child {
						f.exec(f.owner, "ROLLBACK TO timeout_child", 0)
						// O3: ROLLBACK TO unchanged positive setting does not rearm.
						timeoutReferenceIdle(t, p.Ctx, 600*time.Millisecond)
						f.rows(f.owner, 7, 8)
						// A real off/on assignment does rearm the recovered owner.
						f.set("transaction_timeout", "0")
						f.set("transaction_timeout", "250ms")
						f.awaitTransactionFatal(p.Ctx)
						f.dead()
					} else {
						f.exec(f.owner, "ROLLBACK", 0)
						f.show("transaction_timeout", "500ms")
						if fixture.SettingsPhysical(t, f.owner) != identity {
							t.Fatal("ordinary ERROR killed owner")
						}
					}
					f.rows(f.peer, 7)
				})
			})
		}
	}
}

// Compile-time public surface assertion: Go transaction and SQL owner routes
// above share referenceExecutor, rather than an engine-private timer API.
var _ fixture.Executor = (*sql.Tx)(nil)
