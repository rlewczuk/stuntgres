package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionDetachConcurrentVisibility(t *testing.T) {
	for _, isolation := range []sql.IsolationLevel{sql.LevelReadCommitted, sql.LevelRepeatableRead} {
		for _, release := range []string{"commit", "rollback"} {
			for _, held := range []bool{false, true} {
				t.Run(isolation.String()+"/"+release+"/"+map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
					detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						detachConcurrentSetup(run)
						reader := detachConcurrentConn(t, p, b)
						pid := detachConcurrentPID(t, p, b, reader)
						tx, err := reader.BeginTx(p.Ctx, &sql.TxOptions{Isolation: isolation})
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { tx.Rollback() })
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, detachConcurrentRows("dc_parent", 1, 2))
						query := "SELECT k FROM dc_parent ORDER BY k"
						cached, err := tx.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { cached.Close() })
						fresh, err := b.DB.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { fresh.Close() })
						eager, err := tx.QueryContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						// PostgreSQL streams; consume before issuing another command on its owner.
						if b.PG {
							eager.Close()
						} else {
							t.Cleanup(func() { eager.Close() })
						}
						w := detachConcurrentStart(t, p, b, "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY", held)
						fixture.PartitionDetachPending(t, p, b, "dc_parent", "dc_leave", w.pid, pid, w.finished)
						run(detachConcurrentRows("dc_parent", 1))
						detachConcurrentState(run, true, true)
						// Pinned18.6 discovery: fresh inquiry traversal excludes pending children
						// even though the canonical membership and partition bound remain.
						run(fixture.Select("SELECT relid::TEXT,parentrelid::TEXT,isleaf,level FROM pg_partition_tree('dc_parent') ORDER BY relid::TEXT", []string{"relid", "parentrelid", "isleaf", "level"}, []string{"TEXT", "TEXT", "BOOL", "INT4"}, [][]any{{"dc_keep", "dc_parent", true, int64(1)}, {"dc_parent", nil, false, int64(0)}}))
						run(fixture.Select("SELECT pg_partition_root('dc_leave')::TEXT AS root", []string{"root"}, []string{"TEXT"}, [][]any{{"dc_leave"}}))
						values := []int64{1}
						if isolation == sql.LevelRepeatableRead {
							values = append(values, 2)
						}
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, detachConcurrentRows("dc_parent", values...))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: tx, Statement: cached, SQL: query}, b.Recorder, detachConcurrentRows("dc_parent", values...))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: fresh, SQL: query}, b.Recorder, detachConcurrentRows("dc_parent", 1))
						run(fixture.ImplicitError("INSERT INTO dc_parent VALUES(3,'fresh')", fixture.Diagnostic{Code: "23514", Message: `no partition of relation "dc_parent" found for row`, Detail: "Partition key of the failing row contains (k) = (3).", Schema: "public", Table: "dc_parent"}))
						if isolation == sql.LevelRepeatableRead {
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SAVEPOINT dc_route", 0))
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO dc_parent VALUES(3,'old')", 1))
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, detachConcurrentRows("dc_leave", 2, 3))
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ROLLBACK TO dc_route", 0))
						}
						if !b.PG {
							for _, want := range []int64{1, 2} {
								var got int64
								if !eager.Next() {
									t.Fatal("lost eager row", eager.Err())
								}
								if err := eager.Scan(&got); err != nil || got != want {
									t.Fatal(got, err)
								}
							}
							if eager.Next() || eager.Err() != nil {
								t.Fatal("eager tail", eager.Err())
							}
							eager.Close()
						}
						cached.Close()
						if release == "commit" {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						detachConcurrentSuccess(t, w.join())
						detachConcurrentState(run, false, false)
						run(detachConcurrentRows("dc_parent", 1))
					})
				})
			}
		}
	}
}

func TestReferencePartitionDetachConcurrentLocks(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				detachConcurrentSetup(run)
				c := detachConcurrentConn(t, p, b)
				pid := detachConcurrentPID(t, p, b, c)
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { tx.Rollback() })
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, detachConcurrentRows("dc_parent", 1, 2))
				w := detachConcurrentStart(t, p, b, "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY", held)
				fixture.PartitionDetachPending(t, p, b, "dc_parent", "dc_leave", w.pid, pid, w.finished)
				later, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { later.Rollback() })
				fixture.RunStep(t, p.Ctx, later, b.Recorder, detachConcurrentRows("dc_parent", 1))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				// Later parent AS remains live: it must not extend the frozen cohort.
				detachConcurrentSuccess(t, w.join())
				fixture.RunStep(t, p.Ctx, later, b.Recorder, detachConcurrentRows("dc_parent", 1))
				detachConcurrentState(run, false, false)
			})
		})
	}
}
