package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationDeadlockDiagnosticsPreparation(t *testing.T) {
	for _, phase := range []string{"direct", "prepare", "cached"} {
		for _, tc := range []struct{ create, q, position string }{
			{"CREATE TABLE t(id INT,v INT)", "SELECT v FROM t", "15"},
			{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,7)", "13"},
			{"CREATE TABLE t(id INT,v INT)", "UPDATE t SET v=7 WHERE id=1", "8"},
			{"CREATE TABLE t(id INT,v INT)", "DELETE FROM t WHERE id=1", "13"},
			{"CREATE TABLE t(id INT,v INT)", " SELECT v FROM public.t", "16"},
			{"CREATE TABLE t(id INT,v INT)", "  SELECT v FROM public.t", "17"},
			{"CREATE TABLE \"猫\"(id INT,v INT)", "/*猫*/ SELECT v FROM \"猫\"", "21"},
			{"CREATE TABLE t(id INT,v INT)", "SELECT v FROM t FOR KEY SHARE", "15"},
			{"CREATE TABLE t(id INT,v INT)", "SELECT v FROM t FOR SHARE", "15"},
			{"CREATE TABLE t(id INT,v INT)", "SELECT v FROM t FOR NO KEY UPDATE", "15"},
			{"CREATE TABLE t(id INT,v INT)", "SELECT v FROM t FOR UPDATE", "15"},
			{"CREATE TABLE t(id INT,v INT)", "/*é*/ SELECT v FROM public.t FOR KEY SHARE", "21"},
			{"CREATE TABLE t(id INT,v INT)", "/*é*/ SELECT v FROM public.t FOR SHARE", "21"},
			{"CREATE TABLE t(id INT,v INT)", "/*é*/ SELECT v FROM public.t FOR NO KEY UPDATE", "21"},
			{"CREATE TABLE t(id INT,v INT)", "/*é*/ SELECT v FROM public.t FOR UPDATE", "21"},
		} {
			t.Run(phase+"/"+tc.q, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						ctx := p.Ctx
						a, b := fixture.LockConnections(t, ctx, backend.DB)
						fixture.LockExec(t, ctx, a, tc.create, 0)
						fixture.LockExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
						name := "t"
						if strings.Contains(tc.create, "猫") {
							name = "\"猫\""
						}
						ap, bp := 0, 0
						var target, other, database int64
						if backend.PG {
							ap = fixture.LockPID(t, ctx, a)
							bp = fixture.LockPID(t, ctx, b)
							target = fixture.LockOID(t, ctx, "public."+name)
							other = fixture.LockOID(t, ctx, "public.u")
							if err := fixture.Admin.QueryRowContext(ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&database); err != nil {
								t.Fatal(err)
							}
							// Oracle-only detector orchestration forces analysis closing waiter B to
							// discover the cycle, rather than weakening Position for a utility victim.
							fixture.LockExec(t, ctx, a, "SET deadlock_timeout='30s'", 0)
							fixture.LockExec(t, ctx, b, "SET deadlock_timeout='100ms'", 0)
						}
						var cached *sql.Stmt
						if phase == "cached" {
							var err error
							cached, err = b.PrepareContext(ctx, tc.q)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { cached.Close() })
						}
						fixture.LockExec(t, ctx, a, "BEGIN", 0)
						fixture.LockExec(t, ctx, b, "BEGIN", 0)
						t.Cleanup(func() { deadlockPublicRollback(a); deadlockPublicRollback(b) })
						fixture.LockExec(t, ctx, a, "LOCK TABLE "+name+" IN ACCESS EXCLUSIVE MODE", 0)
						fixture.LockExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", 0)
						fixture.LockExec(t, ctx, b, "INSERT INTO u VALUES(202)", 1)
						first := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							r, err := a.ExecContext(ctx, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						if backend.PG {
							fixture.LockWaitBehind(t, ctx, ap, bp, other, "AccessExclusiveLock")
						}
						second := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							switch phase {
							case "prepare":
								s, err := b.PrepareContext(ctx, tc.q)
								return fixture.RelationOutcome{Stmt: s, Err: err}
							case "cached":
								r, err := cached.ExecContext(ctx)
								return fixture.RelationOutcome{Result: r, Err: err}
							default:
								r, err := b.ExecContext(ctx, tc.q)
								return fixture.RelationOutcome{Result: r, Err: err}
							}
						})
						outcomes := []fixture.RelationOutcome{first(), second()}
						victim := -1
						for i, o := range outcomes {
							if o.Err != nil {
								if victim >= 0 {
									t.Fatalf("two victims: %v / %v", outcomes[0].Err, outcomes[1].Err)
								}
								victim = i
								if o.Stmt != nil || o.Result != nil {
									t.Fatal("victim returned handle/result")
								}
							} else if o.Stmt != nil {
								o.Stmt.Close()
							}
						}
						if victim < 0 {
							t.Fatal("no victim")
						}
						if backend.PG && victim != 1 {
							t.Fatal("oracle detector orchestration did not produce analysis victim")
						}
						position := ""
						if victim == 1 && phase != "cached" {
							position = tc.position
						}
						if !backend.PG {
							fixture.DeadlockPublicError(t, outcomes[victim].Err, position)
						} else {
							d := fixture.ExtractDiagnostic(t, outcomes[victim].Err)
							if d.Code != "40P01" || d.Message != "deadlock detected" || d.Position != position {
								t.Fatalf("analysis diagnostic %#v want position %s", d, position)
							}
							mode := "RowExclusiveLock"
							if strings.Contains(tc.q, "SELECT") {
								mode = "AccessShareLock"
								if strings.Contains(tc.q, " FOR ") {
									mode = "RowShareLock"
								}
							}
							referenceDeadlockAnalysisPhysical(t, d, []int{ap, bp}, target, other, database, mode)
							fixture.State(t, outcomes[victim].Err, "40P01")
						}
						if victim == 1 {
							fixture.LockAffected(t, outcomes[0].Result, 0)
						}
						conns := []*sql.Conn{a, b}
						deadlockPublicAborted(t, ctx, conns[victim], "SELECT v FROM u")
						fixture.LockExec(t, ctx, conns[victim], "ROLLBACK", 0)
						fixture.LockExec(t, ctx, conns[1-victim], "ROLLBACK", 0)
						deadlockPublicRows(t, ctx, a, backend.PG, "SELECT v FROM u", nil)
						fixture.LockExec(t, ctx, b, "BEGIN", 0)
						fixture.LockExec(t, ctx, b, "LOCK TABLE "+name+" IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
						fixture.LockExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
						fixture.LockExec(t, ctx, b, "ROLLBACK", 0)
					})
				}
			})
		}
	}
}

func referenceDeadlockAnalysisPhysical(t *testing.T, d fixture.Diagnostic, pids []int, target, other, database int64, mode string) {
	t.Helper()
	if d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" || d.Hint != "See server log for query details." || d.Context != "" || d.InternalPosition != "" || d.InternalQuery != "" || d.Schema != "" || d.Table != "" || d.Column != "" || d.DataType != "" || d.Constraint != "" {
		t.Fatalf("analysis physical fields %#v", d)
	}
	lines := strings.Split(strings.TrimSpace(d.Detail), "\n")
	if len(lines) != 2 {
		t.Fatal("physical cycle edge count", d.Detail)
	}
	want := map[string]bool{
		fmt.Sprintf("Process %d waits for AccessExclusiveLock on relation %d of database %d; blocked by process %d.", pids[0], other, database, pids[1]): true,
		fmt.Sprintf("Process %d waits for %s on relation %d of database %d; blocked by process %d.", pids[1], mode, target, database, pids[0]):           true,
	}
	for _, line := range lines {
		if !want[line] {
			t.Fatalf("actual relation/PID cycle edge %q", line)
		}
		delete(want, line)
	}
	if len(want) != 0 {
		t.Fatal("missing physical cycle edge", want)
	}
}

func TestReferenceRelationDeadlockAcyclic(t *testing.T) {
	for _, ends := range [][3]string{{"ROLLBACK", "ROLLBACK", "ROLLBACK"}, {"COMMIT", "COMMIT", "COMMIT"}, {"COMMIT", "ROLLBACK", "COMMIT"}} {
		t.Run(strings.Join(ends[:], "/"), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx := p.Ctx
					a, b := fixture.LockConnections(t, ctx, backend.DB)
					c, err := backend.DB.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { c.Close() })
					fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					fixture.LockExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
					ap, bp, cp := 0, 0, 0
					var x, y int64
					if backend.PG {
						ap = fixture.LockPID(t, ctx, a)
						bp = fixture.LockPID(t, ctx, b)
						cp = fixture.LockPID(t, ctx, c)
						x = fixture.LockOID(t, ctx, "public.t")
						y = fixture.LockOID(t, ctx, "public.u")
					}
					for _, conn := range []*sql.Conn{a, b, c} {
						fixture.LockExec(t, ctx, conn, "BEGIN", 0)
						t.Cleanup(func() { deadlockPublicRollback(conn) })
					}
					fixture.LockExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					fixture.LockExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", 0)
					rb := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						r, err := b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					if backend.PG {
						fixture.LockWaitBehind(t, ctx, bp, ap, x, "AccessExclusiveLock")
					}
					rc := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						r, err := c.ExecContext(ctx, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					if backend.PG {
						fixture.LockWaitBehind(t, ctx, cp, bp, y, "AccessExclusiveLock")
					}
					fixture.LockExec(t, ctx, a, ends[0], 0)
					o := rb()
					if o.Err != nil {
						t.Fatal("acyclic B", o.Err)
					}
					fixture.LockAffected(t, o.Result, 0)
					fixture.LockExec(t, ctx, b, ends[1], 0)
					o = rc()
					if o.Err != nil {
						t.Fatal("acyclic C", o.Err)
					}
					fixture.LockAffected(t, o.Result, 0)
					fixture.LockExec(t, ctx, c, ends[2], 0)
				})
			}
		})
	}
}
