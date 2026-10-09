package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"
)

func serializableReferenceWriters(t *testing.T, p *referencePair, b referenceBackend) (*sql.Conn, *sql.Conn, *sql.Conn) {
	t.Helper()
	a, c := referenceLockConnections(t, p.ctx, b.db)
	r, err := b.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.ExecContext(context.Background(), "ROLLBACK"); r.Close() })
	run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
	for _, table := range []string{"x", "y"} {
		run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
		run(a, referenceExec("INSERT INTO "+table+" VALUES(0)", 1))
	}
	run(c, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
	run(c, referenceTransactionRows(false, "SELECT v FROM x", 0))
	run(c, referenceTransactionRows(false, "SELECT v FROM y", 0))
	run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
	run(a, referenceTransactionRows(false, "SELECT v FROM y", 0))
	run(a, referenceExec("UPDATE y SET v=20", 1))
	run(a, referenceExec("COMMIT", 0))
	return a, c, r
}

func TestReferenceSerializableReadOnlyAnomaly(t *testing.T) {
	for _, reader := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_reader", true: "ordinary_reader"}[reader], func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c, r := serializableReferenceWriters(t, p, b)
				run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
				if reader {
					run(r, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY", 0))
					run(r, referenceTransactionRows(false, "SELECT v FROM x", 0))
					run(r, referenceTransactionRows(false, "SELECT v FROM y", 20))
					run(r, referenceExec("COMMIT", 0))
				}
				err := serializableReferenceWrite(t, p.ctx, c, "UPDATE x SET v=-11", false, 1)
				committed := false
				if err == nil {
					_, err = c.ExecContext(p.ctx, "COMMIT")
					if err != nil {
						serializableReferenceFailure(t, err)
					} else {
						committed = true
					}
				}
				if committed == reader {
					t.Fatal("read-only anomaly/control outcome", committed, reader)
				}
				c.ExecContext(p.ctx, "ROLLBACK")
				v := int64(0)
				if committed {
					v = -11
				}
				run(a, referenceTransactionRows(false, "SELECT v FROM x", v))
				run(a, referenceTransactionRows(false, "SELECT v FROM y", 20))
			})
		})
	}
}

func serializableReferenceSafeWait(t *testing.T, ctx context.Context, pid int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := referenceAdmin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='IPC' AND wait_event='SafeSnapshot')", pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}

func TestReferenceSerializableDeferrable(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		for _, transition := range []bool{false, true} {
			t.Run(map[bool]string{false: "query", true: "prepare"}[prepare]+map[bool]string{false: "/begin", true: "/transition"}[transition], func(t *testing.T) {
				serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c, r := serializableReferenceWriters(t, p, b)
					run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
					pid := 0
					if b.pg {
						pid = referenceLockPID(t, p.ctx, r)
					}
					begin := "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE"
					if transition {
						begin = "BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE NOT DEFERRABLE"
					}
					run(r, referenceExec(begin, 0))
					if transition {
						run(r, referenceExec("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", 0))
						run(r, referenceExec("SET TRANSACTION READ ONLY", 0))
						run(r, referenceExec("SET TRANSACTION DEFERRABLE", 0))
					}
					await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
						if prepare {
							s, e := r.PrepareContext(ctx, "SELECT v FROM x")
							return relationOutcome{stmt: s, err: e}
						}
						rows, e := r.QueryContext(ctx, "SELECT v FROM x")
						return relationOutcome{rows: rows, err: e}
					})
					if b.pg {
						serializableReferenceSafeWait(t, p.ctx, pid)
					}
					// Native successful rows are not claimed as proof of candidate readiness;
					// the synchronous timeout companion and later internal barriers supply it.
					run(c, referenceExec("UPDATE x SET v=-11", 1))
					run(c, referenceExec("COMMIT", 0))
					o := await()
					if o.err != nil {
						t.Fatal(o.err)
					}
					if prepare {
						if o.stmt == nil {
							t.Fatal("missing safe prepared handle")
						}
						defer o.stmt.Close()
						run(referenceHeldStatement{referenceExecutor: r, statement: o.stmt, query: "SELECT v FROM x"}, referenceTransactionRows(false, "SELECT v FROM x", -11))
					} else {
						if o.rows == nil {
							t.Fatal("missing safe rows")
						}
						types, e := o.rows.ColumnTypes()
						if e != nil || len(types) != 1 || types[0].Name() != "v" || types[0].DatabaseTypeName() != "INT4" {
							o.rows.Close()
							t.Fatal(types, e)
						}
						var v int64
						if !o.rows.Next() {
							o.rows.Close()
							t.Fatal("missing safe row")
						}
						if e := o.rows.Scan(&v); e != nil || v != -11 {
							o.rows.Close()
							t.Fatal(v, e)
						}
						if o.rows.Next() {
							o.rows.Close()
							t.Fatal("extra safe row")
						}
						if e := o.rows.Err(); e != nil {
							o.rows.Close()
							t.Fatal(e)
						}
						o.rows.Close()
					}
					run(r, referenceTransactionRows(false, "SELECT v FROM y", 20))
					run(r, referenceExec("COMMIT", 0))
					run(a, referenceTransactionRows(false, "SELECT v FROM x", -11))
				})
			})
		}
	}
}

func TestReferenceSerializableDeferrableTimeout(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		t.Run(map[bool]string{false: "query", true: "prepare"}[prepare], func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c, r := serializableReferenceWriters(t, p, b)
				run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
				run(r, referenceExec("SET statement_timeout='200ms'", 0))
				run(r, referenceExec("SET lock_timeout='1ms'", 0))
				run(r, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0))
				want := controlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
				if prepare {
					stmt, err := r.PrepareContext(p.ctx, "SELECT v FROM x")
					if stmt != nil {
						stmt.Close()
						t.Fatal("timed-out capture returned handle")
					}
					referenceAssertDiagnostic(t, err, &want)
				} else {
					s := referenceErrorDiagnostic("SELECT v FROM x", want)
					s.selectRows = true
					run(r, s)
				}
				s := referenceErrorDiagnostic("SELECT v FROM y", referenceTransactionAborted())
				s.selectRows = true
				run(r, s)
				run(r, referenceExec("ROLLBACK", 0))
				run(c, referenceExec("ROLLBACK", 0))
				run(r, referenceExec("SET statement_timeout=0", 0))
				run(r, referenceTransactionRows(false, "SELECT v FROM x", 0))
				run(a, referenceTransactionRows(false, "SELECT v FROM y", 20))
			})
		})
	}
}

func TestReferenceSerializableDeferrableCancellation(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		// Native caller-context cancellation requires the implementation's candidate
		// readiness unit barrier; this server observer is a PostgreSQL-only phase.
		if !b.pg {
			return
		}
		_, c, r := serializableReferenceWriters(t, p, b)
		pid := referenceLockPID(t, p.ctx, r)
		run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
		run(r, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0))
		await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
			rows, err := r.QueryContext(ctx, "SELECT v FROM x")
			return relationOutcome{rows: rows, err: err}
		})
		serializableReferenceSafeWait(t, p.ctx, pid)
		var canceled bool
		if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
			t.Fatal(canceled, err)
		}
		o := await()
		if o.rows != nil {
			o.rows.Close()
			t.Fatal("canceled capture returned rows")
		}
		want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
		referenceAssertDiagnostic(t, o.err, &want)
		run(r, referenceExec("ROLLBACK", 0))
		run(c, referenceExec("ROLLBACK", 0))
		if got := referenceLockPID(t, p.ctx, r); got != pid {
			t.Fatal("cancellation replaced physical session", got, pid)
		}
		run(r, referenceTransactionRows(false, "SELECT v FROM x", 0))
	})
}
