//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"regexp"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateCoreDeleteRowContentionOracle(t *testing.T) {
	for _, lane := range []string{"delete_blocks_update", "update_changes_predicate", "update_latest_delete"} {
		for _, commit := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				name := lane + map[bool]string{false: "/rollback", true: "/commit"}[commit] + map[bool]string{false: "/direct", true: "/prepared"}[prepared]
				t.Run(name, func(t *testing.T) {
					p := fixture.NewPair(t)
					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					holder, worker := fixture.LockConnections(t, ctx, p.Postgres)
					peer, err := p.Postgres.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					exec := func(x fixture.Executor, q string, n int64) { fixture.LockExec(t, ctx, x, q, n) }
					rows := func(want [][]any) {
						step := fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want)
						step.Bag = true
						fixture.RunStep(t, ctx, peer, p.PostgresRecord, step)
					}
					exec(peer, "CREATE TABLE t(id INT,v INT)", 0)
					exec(peer, "INSERT INTO t VALUES(1,10),(2,20)", 2)
					hp, wp := fixture.LockPID(t, ctx, holder), fixture.LockPID(t, ctx, worker)
					q, hold := "UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE id=1"
					if lane != "delete_blocks_update" {
						q = "DELETE FROM t WHERE v=10"
						hold = "UPDATE t SET v=20 WHERE id=1"
					}
					if lane == "update_latest_delete" {
						q = "DELETE FROM t WHERE id=1"
					}
					var stmt *sql.Stmt
					if prepared {
						stmt, err = worker.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					for _, conn := range []*sql.Conn{holder, worker} {
						exec(conn, "BEGIN", 0)
						conn := conn
						t.Cleanup(func() {
							cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
							defer stop()
							if _, err := conn.ExecContext(cleanup, "ROLLBACK"); err != nil {
								t.Error(err)
							}
						})
					}
					exec(holder, hold, 1)
					await := fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome {
						var result sql.Result
						var err error
						if prepared {
							result, err = stmt.ExecContext(c)
						} else {
							result, err = worker.ExecContext(c, q)
						}
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					deleteReferenceWaitRow(t, ctx, wp, hp)
					rows([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
					if lane == "update_latest_delete" {
						exec(peer, "UPDATE t SET v=21 WHERE id=2", 1)
					} else {
						exec(peer, "UPDATE t SET v=10 WHERE id=2", 1)
					}
					if commit {
						exec(holder, "COMMIT", 0)
					} else {
						exec(holder, "ROLLBACK", 0)
					}
					outcome := await()
					if outcome.Err != nil {
						t.Fatal(outcome.Err)
					}
					want := int64(1)
					if commit && lane != "update_latest_delete" {
						want = 0
					}
					fixture.LockAffected(t, outcome.Result, want)
					exec(worker, "COMMIT", 0)
					switch lane {
					case "delete_blocks_update":
						if commit {
							rows([][]any{{int64(2), int64(10)}})
						} else {
							rows([][]any{{int64(1), int64(30)}, {int64(2), int64(10)}})
						}
					case "update_changes_predicate":
						if commit {
							rows([][]any{{int64(1), int64(20)}, {int64(2), int64(10)}})
						} else {
							rows([][]any{{int64(2), int64(10)}})
						}
					case "update_latest_delete":
						rows([][]any{{int64(2), int64(21)}})
					}
				})
			}
		}
	}
}

func TestReferenceUpdateCoreDeleteServerCancellation(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "delete_wait", true: "update_wait"}[reverse]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewPair(t)
				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				defer cancel()
				holder, owner := fixture.LockConnections(t, ctx, p.Postgres)
				peer, err := p.Postgres.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				exec := func(x fixture.Executor, q string, n int64) { fixture.LockExec(t, ctx, x, q, n) }
				rows := func(want [][]any) {
					step := fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want)
					step.Bag = true
					fixture.RunStep(t, ctx, peer, p.PostgresRecord, step)
				}
				exec(peer, "CREATE TABLE t(id INT,v INT)", 0)
				exec(peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3)
				hp, op := fixture.LockPID(t, ctx, holder), fixture.LockPID(t, ctx, owner)
				q, hold := "DELETE FROM t WHERE id=3", "UPDATE t SET v=99 WHERE id=3"
				verb := "deleting"
				if reverse {
					q = "UPDATE t SET v=33 WHERE id=3"
					hold = "DELETE FROM t WHERE id=3"
					verb = "updating"
				}
				var stmt *sql.Stmt
				if prepared {
					stmt, err = owner.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				for _, conn := range []*sql.Conn{holder, owner} {
					exec(conn, "BEGIN", 0)
					conn := conn
					t.Cleanup(func() {
						cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
						defer stop()
						if _, err := conn.ExecContext(cleanup, "ROLLBACK"); err != nil {
							t.Error(err)
						}
					})
				}
				exec(holder, hold, 1)
				if reverse {
					exec(owner, "DELETE FROM t WHERE id=1", 1)
					exec(owner, "UPDATE t SET v=22 WHERE id=2", 1)
				} else {
					exec(owner, "UPDATE t SET v=20 WHERE id=1", 1)
					exec(owner, "DELETE FROM t WHERE id=2", 1)
				}
				await := fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome {
					var result sql.Result
					var err error
					if prepared {
						result, err = stmt.ExecContext(c)
					} else {
						result, err = owner.ExecContext(c, q)
					}
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				deleteReferenceWaitRow(t, ctx, op, hp)
				var canceled bool
				if err = fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", op).Scan(&canceled); err != nil || !canceled {
					t.Fatal(canceled, err)
				}
				outcome := await()
				if outcome.Result != nil {
					t.Fatal("canceled DML returned result")
				}
				diagnostic := fixture.ExtractDiagnostic(t, outcome.Err)
				if !regexp.MustCompile(`^while ` + verb + ` tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`).MatchString(diagnostic.Context) {
					t.Fatal("tuple cancellation context", diagnostic)
				}
				fixture.AssertDiagnostic(t, outcome.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: diagnostic.Context})
				_, err = owner.ExecContext(ctx, "UPDATE t SET v=0")
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
				rows([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}})
				want := [][]any{{int64(1), int64(11)}, {int64(3), int64(30)}}
				if reverse {
					exec(peer, "DELETE FROM t WHERE id=1", 1)
					exec(peer, "UPDATE t SET v=21 WHERE id=2", 1)
					want = [][]any{{int64(2), int64(21)}, {int64(3), int64(30)}}
				} else {
					exec(peer, "UPDATE t SET v=11 WHERE id=1", 1)
					exec(peer, "DELETE FROM t WHERE id=2", 1)
				}
				exec(owner, "ROLLBACK", 0)
				exec(holder, "ROLLBACK", 0)
				rows(want)
			})
		}
	}
}
