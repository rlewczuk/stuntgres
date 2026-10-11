package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePreparedRebindAdmission(t *testing.T) {
	for _, kind := range []string{"nested_source", "target"} {
		for _, routeName := range []string{"go", "sql"} {
			t.Run(kind+"/"+routeName, func(t *testing.T) {
				preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					preparedRebindSetup(t, p, b, c)
					query := "SELECT v FROM (SELECT v FROM t) AS d"
					if kind == "target" {
						query = "INSERT INTO t(v) VALUES(12)"
					}
					var r preparedRebindRoute
					if routeName == "go" {
						r = preparedRebindRoute{Executor: preparedRebindHold(t, p, c, query), SQL: query}
					} else {
						sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE rb_admission AS "+query, 0))
						r = preparedRebindRoute{Executor: c, SQL: "EXECUTE rb_admission"}
					}
					if kind == "nested_source" {
						preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
					} else {
						sqlPreparedStep(t, p, b, c, fixture.Exec("BEGIN", 0))
						sqlPreparedStep(t, p, b, r, fixture.Exec(r.SQL, 1))
						sqlPreparedStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
					}
					preparedRebindShadow(t, p, b, c)
					sqlPreparedStep(t, p, b, c, fixture.Exec("UPDATE rb_late.t SET v=8", 1))
					holder, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Close()
					defer holder.ExecContext(context.Background(), "ROLLBACK")
					sqlPreparedStep(t, p, b, holder, fixture.Exec("BEGIN", 0))
					sqlPreparedStep(t, p, b, holder, fixture.Exec("LOCK TABLE rb_early.t IN ACCESS EXCLUSIVE MODE", 0))
					call := func(ctx context.Context) fixture.RelationOutcome {
						if kind == "target" {
							result, err := r.ExecContext(ctx, r.SQL)
							return fixture.RelationOutcome{Result: result, Err: err}
						}
						rows, err := r.QueryContext(ctx, r.SQL)
						return fixture.RelationOutcome{Rows: rows, Err: err}
					}
					// The bounded use must complete while the shadow's AX is still
					// held. Always-fresh admission would queue behind that lock.
					out := fixture.LockOperation(t, p.Ctx, call)()
					if out.Err != nil {
						t.Fatal("prepared use queued on shadow", out.Err)
					}
					if kind == "target" {
						if out.Result == nil {
							t.Fatal("missing write result")
						}
						count, err := out.Result.RowsAffected()
						if err != nil || count != 1 {
							t.Fatal(count, err)
						}
					} else {
						if out.Rows == nil {
							t.Fatal("missing rows")
						}
						defer out.Rows.Close()
						columns, err := out.Rows.ColumnTypes()
						if err != nil {
							out.Rows.Close()
							t.Fatal(err)
						}
						fixture.TableDDLColumnTypes(t, b.PG, columns, []string{"v"}, []string{"INT4"}, []bool{true})
						fixture.TransactionRowValues(t, out.Rows, 8)
					}
					sqlPreparedStep(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
					late := [][]any{{int64(8)}}
					if kind == "target" {
						late = append(late, []any{int64(12)})
					}
					sqlPreparedStep(t, p, b, c, fixture.Select("SELECT v FROM rb_late.t ORDER BY v", []string{"v"}, []string{"INT4"}, late))
					sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT v FROM rb_early.t", "v", "INT4", int64(9)))

					// PostgreSQL exposes its queue via pg_locks and supports server
					// cancellation without losing the session. Native's deterministic
					// queued cancellation is supplemented by the engine helper unit;
					// its public boundary preserves an originating context error.
					pid, holderPID := 0, 0
					var oid int64
					if b.PG {
						pid, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, "rb_late.t")
					}
					sqlPreparedStep(t, p, b, holder, fixture.Exec("BEGIN", 0))
					sqlPreparedStep(t, p, b, holder, fixture.Exec("LOCK TABLE rb_late.t IN ACCESS EXCLUSIVE MODE", 0))
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					if !b.PG {
						cancel()
					}
					join := fixture.LockOperation(t, ctx, call)
					if b.PG {
						mode := "AccessShareLock"
						if kind == "target" {
							mode = "RowExclusiveLock"
						}
						fixture.LockWaitBehind(t, p.Ctx, pid, holderPID, oid, mode)
						var signaled bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
							t.Fatal(signaled, err)
						}
					}
					out = join()
					if out.Rows != nil {
						out.Rows.Close()
						t.Fatal("canceled use returned rows")
					}
					if out.Result != nil {
						t.Fatal("canceled use returned successful result")
					}
					if b.PG {
						d := sqlPreparedDiagnostic("57014", "canceling statement due to user request")
						fixture.AssertDiagnostic(t, out.Err, &d)
					} else if !errors.Is(out.Err, context.Canceled) {
						t.Fatal("ordinary originating cancellation lost", out.Err)
					}
					sqlPreparedStep(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
					sqlPreparedStep(t, p, b, c, fixture.Select("SELECT v FROM rb_late.t ORDER BY v", []string{"v"}, []string{"INT4"}, late))
				})
			})
		}
	}
}
