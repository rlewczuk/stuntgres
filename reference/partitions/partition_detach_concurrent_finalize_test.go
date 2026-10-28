package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionDetachConcurrentFinalize(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, barrier := range []string{"recovery", "child", "older_snapshot", "before_publication", "timeout"} {
			t.Run(barrier+"/"+map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
				detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					detachConcurrentSetup(run)
					if barrier != "before_publication" {
						detachConcurrentPending(t, p, b, held)
						detachConcurrentState(run, true, true)
					}
					if barrier == "recovery" {
						q := "ALTER TABLE dc_parent DETACH PARTITION dc_keep FINALIZE"
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "55000", Message: `cannot complete detaching partition "dc_keep"`, Detail: "There's no pending concurrent detach."})
						if held {
							s = fixture.Prepared(s)
						}
						s.ErrorStage = "execute"
						run(s)
						detachConcurrentState(run, true, true)
						detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", held)
						detachConcurrentState(run, false, false)
						s = fixture.ImplicitError("ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", fixture.Diagnostic{Code: "42P01", Message: `relation "dc_leave" is not a partition of relation "dc_parent"`})
						s.Prepared = held
						s.ErrorStage = "execute"
						run(s)
						return
					}
					c := detachConcurrentConn(t, p, b)
					pid := detachConcurrentPID(t, p, b, c)
					tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { tx.Rollback() })
					if barrier == "older_snapshot" {
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT k FROM dc_keep", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
					} else {
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, detachConcurrentRows("dc_leave", 2))
						if barrier == "before_publication" {
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ALTER TABLE dc_parent DETACH PARTITION dc_keep", 0))
						}
					}
					q := "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE"
					if barrier == "before_publication" {
						q = "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY"
					}
					if barrier == "timeout" {
						owner := detachConcurrentConn(t, p, b)
						var stmt *sql.Stmt
						if held {
							stmt, err = owner.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
						}
						fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("SET lock_timeout='10ms'", 0))
						var e error
						if held {
							_, e = stmt.ExecContext(p.Ctx)
						} else {
							_, e = owner.ExecContext(p.Ctx, q)
						}
						fixture.AssertDiagnostic(t, e, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"})
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						detachConcurrentState(run, true, true)
						return
					}
					w := detachConcurrentStart(t, p, b, q, held)
					if barrier == "older_snapshot" {
						fixture.VirtualCompletionPending(t, p, b, w.pid, pid, w.finished)
					} else {
						relation, mode := "dc_leave", "AccessExclusiveLock"
						if barrier == "before_publication" {
							relation, mode = "dc_parent", "ShareUpdateExclusiveLock"
						}
						oid := int64(0)
						if b.PG {
							oid = fixture.LockOID(t, p.Ctx, relation)
						}
						fixture.RelationLockPending(t, p, b, w.pid, pid, oid, mode, w.finished)
					}
					detachConcurrentCancel(t, p, b, w)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					detachConcurrentState(run, true, barrier != "before_publication")
					if barrier == "before_publication" {
						run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='dc_leave'::regclass AND contype='c' AND conislocal", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					} else {
						detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", held)
						detachConcurrentState(run, false, false)
					}
				})
			})
		}
	}
	detachConcurrentReplacement(t)
}

func TestReferencePartitionDetachConcurrentTransactions(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, plain := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "held"}[held]+"/"+map[bool]string{false: "context", true: "plain"}[plain], func(t *testing.T) {
				detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					detachConcurrentSetup(run)
					detachConcurrentPending(t, p, b, held)
					for _, finish := range []string{"rollback", "savepoint", "commit"} {
						tx, err := b.DB.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { tx.Rollback() })
						own := func(s fixture.Step) { s.NonContext = plain; fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
						if finish == "savepoint" {
							own(fixture.Exec("SAVEPOINT dc_sp", 0))
						}
						q := "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE"
						var e fixture.Executor = tx
						if held {
							var stmt *sql.Stmt
							if plain {
								stmt, err = tx.Prepare(q)
							} else {
								stmt, err = tx.PrepareContext(p.Ctx, q)
							}
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							e = fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}
						}
						step := fixture.Exec(q, 0)
						step.NonContext = plain
						fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
						own(detachConcurrentGraph("dc_parent", "dc_leave", false, false))
						run(detachConcurrentGraph("dc_parent", "dc_leave", true, true))
						if finish == "savepoint" {
							own(fixture.Exec("ROLLBACK TO dc_sp", 0))
							own(detachConcurrentGraph("dc_parent", "dc_leave", true, true))
						}
						if finish == "rollback" {
							err = tx.Rollback()
						} else {
							err = tx.Commit()
						}
						if err != nil {
							t.Fatal(err)
						}
						detachConcurrentState(run, finish != "commit", finish != "commit")
					}
					run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='dc_leave'::regclass AND contype='c' AND conislocal", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
				})
			})
		}
	}
}
