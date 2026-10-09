package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						ctx := p.ctx
						a, b := referenceLockConnections(t, ctx, backend.db)
						referenceLockExec(t, ctx, a, tc.create, 0)
						referenceLockExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
						name := "t"
						if strings.Contains(tc.create, "猫") {
							name = "\"猫\""
						}
						ap, bp := 0, 0
						var target, other, database int64
						if backend.pg {
							ap = referenceLockPID(t, ctx, a)
							bp = referenceLockPID(t, ctx, b)
							target = referenceLockOID(t, ctx, "public."+name)
							other = referenceLockOID(t, ctx, "public.u")
							if err := referenceAdmin.QueryRowContext(ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&database); err != nil {
								t.Fatal(err)
							}
							// Oracle-only detector orchestration forces analysis closing waiter B to
							// discover the cycle, rather than weakening Position for a utility victim.
							referenceLockExec(t, ctx, a, "SET deadlock_timeout='30s'", 0)
							referenceLockExec(t, ctx, b, "SET deadlock_timeout='100ms'", 0)
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
						referenceLockExec(t, ctx, a, "BEGIN", 0)
						referenceLockExec(t, ctx, b, "BEGIN", 0)
						t.Cleanup(func() { deadlockPublicRollback(a); deadlockPublicRollback(b) })
						referenceLockExec(t, ctx, a, "LOCK TABLE "+name+" IN ACCESS EXCLUSIVE MODE", 0)
						referenceLockExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", 0)
						referenceLockExec(t, ctx, b, "INSERT INTO u VALUES(202)", 1)
						first := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
							r, err := a.ExecContext(ctx, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
							return relationOutcome{result: r, err: err}
						})
						if backend.pg {
							referenceLockWaitBehind(t, ctx, ap, bp, other, "AccessExclusiveLock")
						}
						second := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
							switch phase {
							case "prepare":
								s, err := b.PrepareContext(ctx, tc.q)
								return relationOutcome{stmt: s, err: err}
							case "cached":
								r, err := cached.ExecContext(ctx)
								return relationOutcome{result: r, err: err}
							default:
								r, err := b.ExecContext(ctx, tc.q)
								return relationOutcome{result: r, err: err}
							}
						})
						outcomes := []relationOutcome{first(), second()}
						victim := -1
						for i, o := range outcomes {
							if o.err != nil {
								if victim >= 0 {
									t.Fatalf("two victims: %v / %v", outcomes[0].err, outcomes[1].err)
								}
								victim = i
								if o.stmt != nil || o.result != nil {
									t.Fatal("victim returned handle/result")
								}
							} else if o.stmt != nil {
								o.stmt.Close()
							}
						}
						if victim < 0 {
							t.Fatal("no victim")
						}
						if backend.pg && victim != 1 {
							t.Fatal("oracle detector orchestration did not produce analysis victim")
						}
						position := ""
						if victim == 1 && phase != "cached" {
							position = tc.position
						}
						if !backend.pg {
							deadlockPublicError(t, outcomes[victim].err, position)
						} else {
							d := referenceExtractDiagnostic(t, outcomes[victim].err)
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
							referenceState(t, outcomes[victim].err, "40P01")
						}
						if victim == 1 {
							referenceLockAffected(t, outcomes[0].result, 0)
						}
						conns := []*sql.Conn{a, b}
						deadlockPublicAborted(t, ctx, conns[victim], "SELECT v FROM u")
						referenceLockExec(t, ctx, conns[victim], "ROLLBACK", 0)
						referenceLockExec(t, ctx, conns[1-victim], "ROLLBACK", 0)
						deadlockPublicRows(t, ctx, a, backend.pg, "SELECT v FROM u", nil)
						referenceLockExec(t, ctx, b, "BEGIN", 0)
						referenceLockExec(t, ctx, b, "LOCK TABLE "+name+" IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
						referenceLockExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
						referenceLockExec(t, ctx, b, "ROLLBACK", 0)
					})
				}
			})
		}
	}
}

func referenceDeadlockAnalysisPhysical(t *testing.T, d referenceDiagnostic, pids []int, target, other, database int64, mode string) {
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
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					ctx := p.ctx
					a, b := referenceLockConnections(t, ctx, backend.db)
					c, err := backend.db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { c.Close() })
					referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					referenceLockExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
					ap, bp, cp := 0, 0, 0
					var x, y int64
					if backend.pg {
						ap = referenceLockPID(t, ctx, a)
						bp = referenceLockPID(t, ctx, b)
						cp = referenceLockPID(t, ctx, c)
						x = referenceLockOID(t, ctx, "public.t")
						y = referenceLockOID(t, ctx, "public.u")
					}
					for _, conn := range []*sql.Conn{a, b, c} {
						referenceLockExec(t, ctx, conn, "BEGIN", 0)
						t.Cleanup(func() { deadlockPublicRollback(conn) })
					}
					referenceLockExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					referenceLockExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", 0)
					rb := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
						r, err := b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
						return relationOutcome{result: r, err: err}
					})
					if backend.pg {
						referenceLockWaitBehind(t, ctx, bp, ap, x, "AccessExclusiveLock")
					}
					rc := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
						r, err := c.ExecContext(ctx, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
						return relationOutcome{result: r, err: err}
					})
					if backend.pg {
						referenceLockWaitBehind(t, ctx, cp, bp, y, "AccessExclusiveLock")
					}
					referenceLockExec(t, ctx, a, ends[0], 0)
					o := rb()
					if o.err != nil {
						t.Fatal("acyclic B", o.err)
					}
					referenceLockAffected(t, o.result, 0)
					referenceLockExec(t, ctx, b, ends[1], 0)
					o = rc()
					if o.err != nil {
						t.Fatal("acyclic C", o.err)
					}
					referenceLockAffected(t, o.result, 0)
					referenceLockExec(t, ctx, c, ends[2], 0)
				})
			}
		})
	}
}
