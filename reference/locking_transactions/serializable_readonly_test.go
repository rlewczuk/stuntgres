package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func serializableReferenceWriters(t *testing.T, p *fixture.Pair, b fixture.Backend) (*sql.Conn, *sql.Conn, *sql.Conn) {
	t.Helper()
	a, c := fixture.LockConnections(t, p.Ctx, b.DB)
	r, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.ExecContext(context.Background(), "ROLLBACK"); r.Close() })
	run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
	for _, table := range []string{"x", "y"} {
		run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
		run(a, fixture.Exec("INSERT INTO "+table+" VALUES(0)", 1))
	}
	run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
	run(c, fixture.TransactionRows(false, "SELECT v FROM x", 0))
	run(c, fixture.TransactionRows(false, "SELECT v FROM y", 0))
	run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
	run(a, fixture.TransactionRows(false, "SELECT v FROM y", 0))
	run(a, fixture.Exec("UPDATE y SET v=20", 1))
	run(a, fixture.Exec("COMMIT", 0))
	return a, c, r
}

func TestReferenceSerializableReadOnlyAnomaly(t *testing.T) {
	for _, reader := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_reader", true: "ordinary_reader"}[reader], func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c, r := serializableReferenceWriters(t, p, b)
				run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
				if reader {
					run(r, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY", 0))
					run(r, fixture.TransactionRows(false, "SELECT v FROM x", 0))
					run(r, fixture.TransactionRows(false, "SELECT v FROM y", 20))
					run(r, fixture.Exec("COMMIT", 0))
				}
				err := serializableReferenceWrite(t, p.Ctx, c, "UPDATE x SET v=-11", false, 1)
				committed := false
				if err == nil {
					_, err = c.ExecContext(p.Ctx, "COMMIT")
					if err != nil {
						serializableReferenceFailure(t, err)
					} else {
						committed = true
					}
				}
				if committed == reader {
					t.Fatal("read-only anomaly/control outcome", committed, reader)
				}
				c.ExecContext(p.Ctx, "ROLLBACK")
				v := int64(0)
				if committed {
					v = -11
				}
				run(a, fixture.TransactionRows(false, "SELECT v FROM x", v))
				run(a, fixture.TransactionRows(false, "SELECT v FROM y", 20))
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
		if err := fixture.Admin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='IPC' AND wait_event='SafeSnapshot')", pid).Scan(&waiting); err != nil {
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
				serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c, r := serializableReferenceWriters(t, p, b)
					run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
					pid := 0
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, r)
					}
					begin := "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE"
					if transition {
						begin = "BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE NOT DEFERRABLE"
					}
					run(r, fixture.Exec(begin, 0))
					if transition {
						run(r, fixture.Exec("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", 0))
						run(r, fixture.Exec("SET TRANSACTION READ ONLY", 0))
						run(r, fixture.Exec("SET TRANSACTION DEFERRABLE", 0))
					}
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						if prepare {
							s, e := r.PrepareContext(ctx, "SELECT v FROM x")
							return fixture.RelationOutcome{Stmt: s, Err: e}
						}
						rows, e := r.QueryContext(ctx, "SELECT v FROM x")
						return fixture.RelationOutcome{Rows: rows, Err: e}
					})
					if b.PG {
						serializableReferenceSafeWait(t, p.Ctx, pid)
					}
					// Native successful rows are not claimed as proof of candidate readiness;
					// the synchronous timeout companion and later internal barriers supply it.
					run(c, fixture.Exec("UPDATE x SET v=-11", 1))
					run(c, fixture.Exec("COMMIT", 0))
					o := await()
					if o.Err != nil {
						t.Fatal(o.Err)
					}
					if prepare {
						if o.Stmt == nil {
							t.Fatal("missing safe prepared handle")
						}
						defer o.Stmt.Close()
						run(fixture.HeldStatement{Executor: r, Statement: o.Stmt, SQL: "SELECT v FROM x"}, fixture.TransactionRows(false, "SELECT v FROM x", -11))
					} else {
						if o.Rows == nil {
							t.Fatal("missing safe rows")
						}
						types, e := o.Rows.ColumnTypes()
						if e != nil || len(types) != 1 || types[0].Name() != "v" || types[0].DatabaseTypeName() != "INT4" {
							o.Rows.Close()
							t.Fatal(types, e)
						}
						var v int64
						if !o.Rows.Next() {
							o.Rows.Close()
							t.Fatal("missing safe row")
						}
						if e := o.Rows.Scan(&v); e != nil || v != -11 {
							o.Rows.Close()
							t.Fatal(v, e)
						}
						if o.Rows.Next() {
							o.Rows.Close()
							t.Fatal("extra safe row")
						}
						if e := o.Rows.Err(); e != nil {
							o.Rows.Close()
							t.Fatal(e)
						}
						o.Rows.Close()
					}
					run(r, fixture.TransactionRows(false, "SELECT v FROM y", 20))
					run(r, fixture.Exec("COMMIT", 0))
					run(a, fixture.TransactionRows(false, "SELECT v FROM x", -11))
				})
			})
		}
	}
}

func TestReferenceSerializableDeferrableTimeout(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		t.Run(map[bool]string{false: "query", true: "prepare"}[prepare], func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c, r := serializableReferenceWriters(t, p, b)
				run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
				run(r, fixture.Exec("SET statement_timeout='200ms'", 0))
				run(r, fixture.Exec("SET lock_timeout='1ms'", 0))
				run(r, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0))
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
				if prepare {
					stmt, err := r.PrepareContext(p.Ctx, "SELECT v FROM x")
					if stmt != nil {
						stmt.Close()
						t.Fatal("timed-out capture returned handle")
					}
					fixture.AssertDiagnostic(t, err, &want)
				} else {
					s := fixture.ErrorDiagnostic("SELECT v FROM x", want)
					s.SelectRows = true
					run(r, s)
				}
				s := fixture.ErrorDiagnostic("SELECT v FROM y", fixture.TransactionAborted())
				s.SelectRows = true
				run(r, s)
				run(r, fixture.Exec("ROLLBACK", 0))
				run(c, fixture.Exec("ROLLBACK", 0))
				run(r, fixture.Exec("SET statement_timeout=0", 0))
				run(r, fixture.TransactionRows(false, "SELECT v FROM x", 0))
				run(a, fixture.TransactionRows(false, "SELECT v FROM y", 20))
			})
		})
	}
}

func TestReferenceSerializableDeferrableCancellation(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Native caller-context cancellation requires the implementation's candidate
		// readiness unit barrier; this server observer is a PostgreSQL-only phase.
		if !b.PG {
			return
		}
		_, c, r := serializableReferenceWriters(t, p, b)
		pid := fixture.LockPID(t, p.Ctx, r)
		run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
		run(r, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0))
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := r.QueryContext(ctx, "SELECT v FROM x")
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		serializableReferenceSafeWait(t, p.Ctx, pid)
		var canceled bool
		if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
			t.Fatal(canceled, err)
		}
		o := await()
		if o.Rows != nil {
			o.Rows.Close()
			t.Fatal("canceled capture returned rows")
		}
		want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
		fixture.AssertDiagnostic(t, o.Err, &want)
		run(r, fixture.Exec("ROLLBACK", 0))
		run(c, fixture.Exec("ROLLBACK", 0))
		if got := fixture.LockPID(t, p.Ctx, r); got != pid {
			t.Fatal("cancellation replaced physical session", got, pid)
		}
		run(r, fixture.TransactionRows(false, "SELECT v FROM x", 0))
	})
}
