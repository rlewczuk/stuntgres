package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func smokeDeadlockKinds(t *testing.T, kinds []string) {
	for _, kind := range kinds {
		for _, cached := range []bool{false, true} {
			t.Run(kind+"/"+fmt.Sprint(cached), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						deadlockPublicCycle(t, p.Ctx, backend.DB, kind, cached, backend.PG, nil, func(err error, _ int, _ string) {
							d := fixture.DeadlockPublicError(t, err, "")
							if kind != "table" && !strings.Contains(d.Detail, " row ") {
								t.Fatal("row identity absent", d.Detail)
							}
						})
					})
				}
			})
		}
	}
}

func smokeDeadlockPreparation(t *testing.T) {
	for _, phase := range []string{"direct", "prepare", "cached"} {
		for _, tc := range []struct{ q, pos string }{{"SELECT v FROM t", "15"}, {"INSERT INTO t VALUES(1,7)", "13"}, {"UPDATE t SET v=7 WHERE id=1", "8"}, {"DELETE FROM t WHERE id=1", "13"}, {" SELECT v FROM public.t", "16"}} {
			t.Run(phase+"/"+tc.q, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						db := backend.DB
						ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
						t.Cleanup(cancel)
						a := deadlockPublicConn(t, ctx, db)
						b := deadlockPublicConn(t, ctx, db)
						deadlockPublicExec(t, ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
						deadlockPublicExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
						var cached *sql.Stmt
						if phase == "cached" {
							var err error
							cached, err = b.PrepareContext(ctx, tc.q)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { cached.Close() })
						}
						for _, c := range []*sql.Conn{a, b} {
							deadlockPublicExec(t, ctx, c, "BEGIN", 0)
							t.Cleanup(func() { deadlockPublicRollback(c) })
						}
						deadlockPublicExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						deadlockPublicExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", 0)
						ap, bp := 0, 0
						var target, other, database int64
						if backend.PG {
							ap, bp = fixture.LockPID(t, ctx, a), fixture.LockPID(t, ctx, b)
							target, other = fixture.LockOID(t, ctx, "public.t"), fixture.LockOID(t, ctx, "public.u")
							if err := fixture.Admin.QueryRowContext(ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&database); err != nil {
								t.Fatal(err)
							}
							deadlockPublicExec(t, ctx, a, "SET deadlock_timeout='30s'", 0)
							deadlockPublicExec(t, ctx, b, "SET deadlock_timeout='100ms'", 0)
						}
						outcomesCtx, outcomesCancel := context.WithTimeout(ctx, 8*time.Second)
						defer outcomesCancel()
						first := deadlockPublicWorker(t, outcomesCtx, 0, func(ctx context.Context) (sql.Result, error) {
							return a.ExecContext(ctx, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
						})
						if backend.PG {
							fixture.LockWaitBehind(t, ctx, ap, bp, other, "AccessExclusiveLock")
						}
						second := deadlockPublicWorker(t, outcomesCtx, 1, func(ctx context.Context) (sql.Result, error) {
							switch phase {
							case "prepare":
								s, err := b.PrepareContext(ctx, tc.q)
								if s != nil {
									s.Close()
									if err != nil {
										return nil, fmt.Errorf("failed Prepare returned a handle")
									}
								}
								return nil, err
							case "cached":
								return cached.ExecContext(ctx)
							default:
								return b.ExecContext(ctx, tc.q)
							}
						})
						outcomes := []deadlockPublicOutcome{first(), second()}
						victim := -1
						for i, o := range outcomes {
							if o.err != nil {
								if victim >= 0 {
									t.Fatalf("two victims: %v %v", outcomes[0].err, outcomes[1].err)
								}
								victim = i
								position := ""
								if i == 1 && phase != "cached" {
									position = tc.pos
								}
								if o.result != nil {
									t.Fatal("victim returned result")
								}
								if !backend.PG {
									fixture.DeadlockPublicError(t, o.err, position)
								} else {
									d := fixture.ExtractDiagnostic(t, o.err)
									if d.Code != "40P01" || d.Message != "deadlock detected" || d.Position != position {
										t.Fatalf("analysis diagnostic %#v, want position %s", d, position)
									}
									mode := "RowExclusiveLock"
									if strings.Contains(tc.q, "SELECT") {
										mode = "AccessShareLock"
									}
									referenceDeadlockAnalysisPhysical(t, d, []int{ap, bp}, target, other, database, mode)
									fixture.State(t, o.err, "40P01")
								}
							}
						}
						if victim < 0 {
							t.Fatal("no victim")
						}
						deadlockPublicAborted(t, ctx, []*sql.Conn{a, b}[victim], "SELECT v FROM u")
						deadlockPublicExec(t, ctx, []*sql.Conn{a, b}[victim], "ROLLBACK", 0)
						deadlockPublicExec(t, ctx, []*sql.Conn{a, b}[1-victim], "ROLLBACK", 0)
						if victim == 1 {
							deadlockPublicCount(t, outcomes[0].result, 0)
						}
						deadlockPublicRows(t, ctx, a, backend.PG, "SELECT v FROM u", nil)
						deadlockPublicExec(t, ctx, b, "BEGIN", 0)
						deadlockPublicExec(t, ctx, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
						deadlockPublicExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
						deadlockPublicExec(t, ctx, b, "ROLLBACK", 0)
					})
				}
			})
		}
	}
}

func TestReferenceSmokeRelationDeadlockSmokeTable(t *testing.T) {
	smokeDeadlockKinds(t, []string{"table"})
	smokeDeadlockPreparation(t)
}
func TestReferenceSmokeRelationDeadlockSmokeRowsMixed(t *testing.T) {
	smokeDeadlockKinds(t, []string{"rows", "delete", "partial", "mixed"})
}
func TestReferenceSmokeRelationDeadlockSmokeSoftQueue(t *testing.T) {
	t.Run("soft", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) { deadlockPublicSoftQueue(t, p.Ctx, backend.DB, backend.PG, nil) })
		}
	})
	t.Run("acyclic", smokeDeadlockAcyclic)
}
