package reference_test

import (
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
				identity := settingsPhysical(t, f.owner)
				if kind == "local-rollback" || kind == "local-commit" || kind == "session-commit" {
					f.exec(f.owner, "BEGIN", 0)
					mode := "LOCAL"
					if kind == "session-commit" {
						mode = "SESSION"
					}
					f.exec(f.owner, "SET "+mode+" transaction_timeout='250ms'", 0)
					end := "COMMIT"
					if kind == "local-rollback" {
						end = "ROLLBACK"
					}
					f.exec(f.owner, end, 0)
					timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
					want := "0"
					if kind == "session-commit" {
						want = "250ms"
					}
					f.show("transaction_timeout", want)
					if settingsPhysical(t, f.owner) != identity {
						t.Fatal("completed transaction lost physical owner")
					}
					f.rows(f.peer, 7)
					return
				}
				f.set("transaction_timeout", "250ms")
				f.exec(f.owner, "BEGIN", 0)
				switch kind {
				case "commands":
					for range 2 {
						timeoutReferenceIdle(t, p.Ctx, 50*time.Millisecond)
						f.show("transaction_timeout", "250ms")
					}
				case "positive-positive", "same-value":
					timeoutReferenceIdle(t, p.Ctx, 150*time.Millisecond)
					value := "1s"
					if kind == "same-value" {
						value = "250ms"
					}
					f.set("transaction_timeout", value)
					f.show("transaction_timeout", value)
				case "disable", "reset", "reset-all":
					switch kind {
					case "disable":
						f.set("transaction_timeout", "0")
					case "reset":
						f.exec(f.owner, "RESET transaction_timeout", 0)
					case "reset-all":
						f.exec(f.owner, "RESET ALL", 0)
					}
					timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
					f.show("transaction_timeout", "0")
					f.exec(f.owner, "ROLLBACK", 0)
					f.show("transaction_timeout", "250ms")
					f.rows(f.peer, 7)
					return
				case "reenable", "savepoint-restore", "set-config":
					if kind == "savepoint-restore" {
						f.exec(f.owner, "SAVEPOINT timeout_restore", 0)
					}
					if kind == "set-config" {
						fixture.RunStep(t, p.Ctx, f.owner, b.Recorder, localReferenceSelect(b.PG, "SELECT set_config('transaction_timeout','0',false)", "set_config", "0"))
					} else {
						f.set("transaction_timeout", "0")
					}
					timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
					f.show("transaction_timeout", "0")
					if kind == "savepoint-restore" {
						f.exec(f.owner, "ROLLBACK TO timeout_restore", 0)
					} else {
						f.set("transaction_timeout", "250ms")
					}
					timeoutReferenceIdle(t, p.Ctx, 50*time.Millisecond)
					f.show("transaction_timeout", "250ms")
				}
				delay := 350 * time.Millisecond
				if kind == "commands" {
					delay = 175 * time.Millisecond
				}
				if kind == "positive-positive" || kind == "same-value" {
					delay = 150 * time.Millisecond
				}
				timeoutReferenceIdle(t, p.Ctx, delay)
				if kind == "savepoint-restore" {
					// Restoring SHOW250 after SET0 does not
					// rearm the retired budget. A real later off/on still does.
					f.show("transaction_timeout", "250ms")
					f.rows(f.owner, 7)
					f.set("transaction_timeout", "0")
					f.set("transaction_timeout", "250ms")
					timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
				}
				f.fatalProbe("25P04")
				f.rows(f.peer, 7)
			})
		})
	}
}

func TestReferenceTimeoutTransactionAbortRestore(t *testing.T) {
	for _, failure := range []string{"semantic", "statement", "lock"} {
		for _, child := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/child=%v", failure, child), func(t *testing.T) {
				timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					f := newTimeoutReferenceFixture(t, p, b)
					identity := settingsPhysical(t, f.owner)
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
						timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
						f.fatalProbe("25P04")
					} else {
						f.exec(f.owner, "ROLLBACK", 0)
						f.show("transaction_timeout", "500ms")
						if settingsPhysical(t, f.owner) != identity {
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
