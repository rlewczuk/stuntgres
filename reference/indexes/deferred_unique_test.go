package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredUniqueTransactions(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		for _, repair := range []string{"DELETE FROM t WHERE id=2", "UPDATE t SET v=2 WHERE id=2", ""} {
			for _, goCommit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/go=%t", kind, repair, goCommit), func(t *testing.T) {
					fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE t(id INT,v INT,CONSTRAINT u "+kind+"(v) INITIALLY DEFERRED)", 0))
						var tx *sql.Tx
						var executor fixture.Executor = c
						if goCommit {
							var err error
							tx, err = c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							executor = tx
						} else {
							run(fixture.Exec("BEGIN", 0))
						}
						step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, s) }
						step(fixture.Exec("CREATE TABLE private_marker(v INT)", 0))
						step(fixture.Exec("INSERT INTO t VALUES(1,1)", 1))
						step(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, int64(2), int64(1))))
						step(fixture.Select("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
						if repair != "" {
							step(fixture.Prepared(fixture.Exec(repair, 1)))
						}
						want := fixture.DeferredUniqueConflict("t", "u", "v", "1")
						if goCommit {
							err := tx.Commit()
							if repair == "" {
								fixture.AssertDiagnostic(t, err, &want)
							} else {
								fixture.State(t, err, "")
							}
						} else {
							if repair == "" {
								step(fixture.ErrorDiagnostic("COMMIT", want))
							} else {
								step(fixture.Exec("COMMIT", 0))
							}
						}
						values := [][]any(nil)
						if repair != "" {
							values = [][]any{{int64(1)}}
							if repair[0] == 'U' {
								values = append(values, []any{int64(2)})
							}
						}
						run(fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, values))
						if repair == "" {
							run(fixture.Select("SELECT relname FROM pg_class WHERE relname='private_marker'", []string{"relname"}, []string{"NAME"}, nil))
							run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,1),(2,1)", want))
						}
						run(fixture.Exec("INSERT INTO t VALUES(9,9)", 1))
					})
				})
			}
		}
	}
}

func TestReferenceDeferredUniqueStatementTiming(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		for _, mode := range []string{"NOT DEFERRABLE", "DEFERRABLE INITIALLY IMMEDIATE"} {
			t.Run(kind+mode, func(t *testing.T) {
				fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u "+kind+"(v) "+mode+")", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
					if mode == "NOT DEFERRABLE" {
						run(fixture.ErrorDiagnostic("UPDATE t SET v=v+1", fixture.DeferredUniqueConflict("t", "u", "v", "2")))
						run(fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					} else {
						run(fixture.Prepared(fixture.Exec("UPDATE t SET v=v+1", 2)))
						run(fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
					}
				})
			})
		}
	}
	for _, nulls := range []string{"DISTINCT", "NOT DISTINCT"} {
		t.Run(nulls, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u UNIQUE NULLS "+nulls+"(v) INITIALLY DEFERRED)", 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO t VALUES(NULL),(NULL)", 2))
				if nulls == "DISTINCT" {
					run(fixture.Exec("COMMIT", 0))
				} else {
					run(fixture.ErrorDiagnostic("COMMIT", fixture.DeferredUniqueConflict("t", "u", "v", "null")))
				}
			})
		})
	}
}

func TestReferenceDeferredUniqueConcurrent(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, run func(fixture.Step)) {
				peer := fixture.ConcurrentIndexConn(t, p, b)
				step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) INITIALLY DEFERRED)", 0))
				run(fixture.Exec("BEGIN", 0))
				step(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				step(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(1)", 1)))
				join := fixture.DefinitionLockOperation(t, p.Ctx, p, b, peer, a, "COMMIT", "transactionid", "ShareLock", 0, 0)
				// First uncontested admission has no retrospective obligation.
				if commit {
					run(fixture.Exec("COMMIT", 0))
				} else {
					run(fixture.Exec("ROLLBACK", 0))
				}
				out := join()
				if commit {
					want := fixture.DeferredUniqueConflict("t", "u", "v", "1")
					fixture.AssertDiagnostic(t, out.Err, &want)
					if out.Result != nil {
						t.Fatal("failed commit returned Result")
					}
				} else {
					fixture.State(t, out.Err, "")
					fixture.LockAffected(t, out.Result, 0)
				}
				run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(fixture.Exec("INSERT INTO t VALUES(9)", 1))
			})
		})
	}
}
