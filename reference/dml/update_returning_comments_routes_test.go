package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningCommentSnapshot(t *testing.T) {
	updateReturningCommentContextReplay(t)
	for _, prepared := range []bool{false, true} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			db := backend.DB
			fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
			fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1)")
			fixture.UpdateReturningReferenceExec(t, db, "COMMENT ON TABLE t IS 'committed table'")
			fixture.UpdateReturningReferenceExec(t, db, "COMMENT ON COLUMN t.id IS 'committed column'")
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
				fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "column_comment", "table_comment"}, Types: []string{"INT4", "TEXT", "TEXT"}, Rows: [][]any{{int64(1), "committed column", "committed table"}}, Bag: false})
				stmt.Close()
			} else {
				rows, err := tx.Query(query)
				fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "column_comment", "table_comment"}, Types: []string{"INT4", "TEXT", "TEXT"}, Rows: [][]any{{int64(1), "committed column", "committed table"}}, Bag: false})
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			rows, err := db.Query("SELECT id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment FROM t")
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "column_comment", "table_comment"}, Types: []string{"INT4", "TEXT", "TEXT"}, Rows: [][]any{{int64(1), "committed column", "committed table"}}, Bag: false})

		}
	}
}

func updateReturningCommentContextReplay(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("COMMENT ON TABLE t IS 'committed table'", 0))
			run(fixture.Exec("COMMENT ON COLUMN t.id IS 'committed column'", 0))
			for _, prepared := range []bool{false, true} {
				tx, err := db.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				s := fixture.Select("UPDATE t SET id=id RETURNING id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment", []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s)
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment FROM t", s.Columns, s.Types, s.Rows))
			}
		})
	}
}
