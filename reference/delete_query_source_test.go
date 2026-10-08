package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDeleteCoreInsertQuerySource(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared_%t/commit_%t", prepared, commit), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						db, r := backend.db, backend.recorder
						a, b := referenceLockConnections(t, p.ctx, db)
						run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, r, s) }
						for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(1),(2),(2),(3)", 4), referenceExec("CREATE TABLE destination(id INT)", 0)} {
							run(a, s)
						}
						q := "SELECT id FROM t ORDER BY id /* retained source */"
						retained, err := db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, r), q)
						if err != nil {
							t.Fatal(err)
						}
						defer retained.Close()
						copySQL := "INSERT INTO destination SELECT id FROM t ORDER BY id"
						held, err := a.PrepareContext(p.ctx, copySQL)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						run(a, referenceExec("DELETE FROM t WHERE id=1", 1))
						var result sql.Result
						if prepared {
							result, err = held.ExecContext(p.ctx)
						} else {
							result, err = a.ExecContext(p.ctx, copySQL)
						}
						deleteReferenceCount(t, result, err, 3)
						run(a, referenceSelect("SELECT id FROM destination ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {int64(3)}}))
						run(a, referenceExec("DELETE FROM destination", 3))
						tx, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						run(tx, referenceExec("INSERT INTO t VALUES(7)", 1))
						run(tx, referenceExec("DELETE FROM t WHERE id=2 OR id=7", 3))
						for _, s := range []referenceStep{referenceExec("INSERT INTO t SELECT id FROM t", 1), referenceExec(copySQL, 2), referenceExec("DELETE FROM t", 2), referenceExec(copySQL, 0)} {
							if prepared {
								stmt, e := tx.PrepareContext(p.ctx, s.query)
								if e != nil {
									t.Fatal(e)
								}
								result, err = stmt.ExecContext(p.ctx)
								if e := stmt.Close(); e != nil {
									t.Fatal(e)
								}
							} else {
								result, err = tx.ExecContext(p.ctx, s.query)
							}
							deleteReferenceCount(t, result, err, s.affected)
						}
						deleteReferenceRows(t, p, tx, r)
						run(tx, referenceSelect("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}}))
						deleteReferenceRows(t, p, b, r, 2, 2, 3)
						run(b, referenceSelect("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{}))
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						if commit {
							deleteReferenceRows(t, p, b, r)
							run(b, referenceSelect("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}}))
						} else {
							deleteReferenceRows(t, p, b, r, 2, 2, 3)
							run(b, referenceSelect("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{}))
						}
						s := referenceSelect(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}})
						deleteReferenceConsume(t, backend.pg, retained, nil, s)
						deleteReferenceRecorded(t, r, s)
					})
				}
			})
		}
	}
}
