package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCoreInsertQuerySource(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared_%t/commit_%t", prepared, commit), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						db, r := backend.DB, backend.Recorder
						a, b := fixture.LockConnections(t, p.Ctx, db)
						run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
						for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(1),(2),(2),(3)", 4), fixture.Exec("CREATE TABLE destination(id INT)", 0)} {
							run(a, s)
						}
						q := "SELECT id FROM t ORDER BY id /* retained source */"
						retained, err := db.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, r), q)
						if err != nil {
							t.Fatal(err)
						}
						defer retained.Close()
						copySQL := "INSERT INTO destination SELECT id FROM t ORDER BY id"
						held, err := a.PrepareContext(p.Ctx, copySQL)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						run(a, fixture.Exec("DELETE FROM t WHERE id=1", 1))
						var result sql.Result
						if prepared {
							result, err = held.ExecContext(p.Ctx)
						} else {
							result, err = a.ExecContext(p.Ctx, copySQL)
						}
						fixture.DeleteReferenceCount(t, result, err, 3)
						run(a, fixture.Select("SELECT id FROM destination ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {int64(3)}}))
						run(a, fixture.Exec("DELETE FROM destination", 3))
						tx, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						run(tx, fixture.Exec("INSERT INTO t VALUES(7)", 1))
						run(tx, fixture.Exec("DELETE FROM t WHERE id=2 OR id=7", 3))
						for _, s := range []fixture.Step{fixture.Exec("INSERT INTO t SELECT id FROM t", 1), fixture.Exec(copySQL, 2), fixture.Exec("DELETE FROM t", 2), fixture.Exec(copySQL, 0)} {
							if prepared {
								stmt, e := tx.PrepareContext(p.Ctx, s.Query)
								if e != nil {
									t.Fatal(e)
								}
								result, err = stmt.ExecContext(p.Ctx)
								if e := stmt.Close(); e != nil {
									t.Fatal(e)
								}
							} else {
								result, err = tx.ExecContext(p.Ctx, s.Query)
							}
							fixture.DeleteReferenceCount(t, result, err, s.Affected)
						}
						fixture.DeleteReferenceRows(t, p, tx, r)
						run(tx, fixture.Select("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}}))
						fixture.DeleteReferenceRows(t, p, b, r, 2, 2, 3)
						run(b, fixture.Select("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{}))
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						if commit {
							fixture.DeleteReferenceRows(t, p, b, r)
							run(b, fixture.Select("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}}))
						} else {
							fixture.DeleteReferenceRows(t, p, b, r, 2, 2, 3)
							run(b, fixture.Select("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{}))
						}
						s := fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}})
						fixture.DeleteReferenceConsume(t, backend.PG, retained, nil, s)
						fixture.DeleteReferenceRecorded(t, r, s)
					})
				}
			})
		}
	}
}
