package reference_test

import "testing"

func TestReferenceDeleteReturningSQLControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db, r := backend.db, backend.recorder
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, r, s) }
			for _, s := range []referenceStep{
				referenceExec("CREATE SEQUENCE s START 101 CACHE 1", 0),
				referenceExec("CREATE TABLE t(id INT NOT NULL DEFAULT 9,v TEXT)", 0),
				referenceExec("COMMENT ON COLUMN t.v IS 'preserved'", 0),
				referenceExec("CREATE VIEW survivors AS SELECT id,v FROM t", 0),
				referenceExec("INSERT INTO t VALUES(1,'a'),(2,NULL),(2,NULL),(3,'c')", 4),
			} {
				run(s)
			}
			tx, err := db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			s := referenceSelect("DELETE FROM t WHERE id=2 RETURNING old.id,new.id,v,nextval('s')", []string{"id", "id", "v", "nextval"}, []string{"INT4", "INT4", "TEXT", "INT8"}, [][]any{{int64(2), nil, nil, int64(101)}, {int64(2), nil, nil, int64(102)}})
			s.bag = true
			referenceRunStep(t, p.ctx, tx, r, s)
			deleteReferenceRows(t, p, db, r, 1, 2, 2, 3)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("DELETE FROM t WHERE id=1 RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(referenceSelect("SELECT id,v FROM survivors ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), nil}, {int64(2), nil}, {int64(3), "c"}}))
			run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(103)}}))
			run(referenceExec("INSERT INTO t(v) VALUES('default')", 1))
			deleteReferenceRows(t, p, db, r, 2, 2, 3, 9)
			failure := referenceImplicitError("INSERT INTO t VALUES(NULL,'bad')", referenceDiagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "id"})
			failure.errorStage = "execute"
			deleteReferenceExecuteStep(t, p, db, r, failure)
			run(referenceSelect("SELECT col_description('t'::regclass,2)", []string{"col_description"}, []string{"TEXT"}, [][]any{{"preserved"}}))
			s = referenceSelect("DELETE FROM t RETURNING *", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), nil}, {int64(2), nil}, {int64(3), "c"}, {int64(9), "default"}})
			s.bag = true
			run(s)
			run(referenceSelect("SELECT id,v FROM survivors", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{}))
		})
	}
}
