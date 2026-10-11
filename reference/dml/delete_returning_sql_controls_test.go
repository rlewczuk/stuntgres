package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteReturningSQLControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, r := backend.DB, backend.Recorder
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, r, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE SEQUENCE s START 101 CACHE 1", 0),
				fixture.Exec("CREATE TABLE t(id INT NOT NULL DEFAULT 9,v TEXT)", 0),
				fixture.Exec("COMMENT ON COLUMN t.v IS 'preserved'", 0),
				fixture.Exec("CREATE VIEW survivors AS SELECT id,v FROM t", 0),
				fixture.Exec("INSERT INTO t VALUES(1,'a'),(2,NULL),(2,NULL),(3,'c')", 4),
			} {
				run(s)
			}
			tx, err := db.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			s := fixture.Select("DELETE FROM t WHERE id=2 RETURNING old.id,new.id,v,nextval('s')", []string{"id", "id", "v", "nextval"}, []string{"INT4", "INT4", "TEXT", "INT8"}, [][]any{{int64(2), nil, nil, int64(101)}, {int64(2), nil, nil, int64(102)}})
			s.Bag = true
			fixture.RunStep(t, p.Ctx, tx, r, s)
			fixture.DeleteReferenceRows(t, p, db, r, 1, 2, 2, 3)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("DELETE FROM t WHERE id=1 RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT id,v FROM survivors ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), nil}, {int64(2), nil}, {int64(3), "c"}}))
			run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(103)}}))
			run(fixture.Exec("INSERT INTO t(v) VALUES('default')", 1))
			fixture.DeleteReferenceRows(t, p, db, r, 2, 2, 3, 9)
			failure := fixture.ImplicitError("INSERT INTO t VALUES(NULL,'bad')", fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "id"})
			failure.ErrorStage = "execute"
			fixture.DeleteReferenceExecuteStep(t, p, db, r, failure)
			run(fixture.Select("SELECT col_description('t'::regclass,2)", []string{"col_description"}, []string{"TEXT"}, [][]any{{"preserved"}}))
			s = fixture.Select("DELETE FROM t RETURNING *", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), nil}, {int64(2), nil}, {int64(3), "c"}, {int64(9), "default"}})
			s.Bag = true
			run(s)
			run(fixture.Select("SELECT id,v FROM survivors", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{}))
		})
	}
}
