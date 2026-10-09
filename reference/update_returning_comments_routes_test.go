package reference_test

import (
	"testing"
)

func TestReferenceUpdateReturningCommentSnapshot(t *testing.T) {
	updateReturningCommentContextReplay(t)
	for _, prepared := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			db := backend.db
			updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
			updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1)")
			updateReturningReferenceExec(t, db, "COMMENT ON TABLE t IS 'committed table'")
			updateReturningReferenceExec(t, db, "COMMENT ON COLUMN t.id IS 'committed column'")
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			query := "UPDATE t SET id=id RETURNING id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment"
			if prepared {
				stmt, err := tx.Prepare(query)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := stmt.Query()
				deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "column_comment", "table_comment"}, types: []string{"INT4", "TEXT", "TEXT"}, rows: [][]any{{int64(1), "committed column", "committed table"}}, bag: false})
				stmt.Close()
			} else {
				rows, err := tx.Query(query)
				deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "column_comment", "table_comment"}, types: []string{"INT4", "TEXT", "TEXT"}, rows: [][]any{{int64(1), "committed column", "committed table"}}, bag: false})
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			rows, err := db.Query("SELECT id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment FROM t")
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "column_comment", "table_comment"}, types: []string{"INT4", "TEXT", "TEXT"}, rows: [][]any{{int64(1), "committed column", "committed table"}}, bag: false})

		}
	}
}

func updateReturningCommentContextReplay(t *testing.T) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceExec("COMMENT ON TABLE t IS 'committed table'", 0))
			run(referenceExec("COMMENT ON COLUMN t.id IS 'committed column'", 0))
			for _, prepared := range []bool{false, true} {
				tx, err := db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				s := referenceSelect("UPDATE t SET id=id RETURNING id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment", []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}})
				s.prepared = prepared
				referenceRunStep(t, p.ctx, tx, backend.recorder, s)
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment FROM t", s.columns, s.types, s.rows))
			}
		})
	}
}
