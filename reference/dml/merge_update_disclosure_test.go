package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// ExecGetInsertedCols UNION ExecGetUpdatedCols supplies diagnostic columns,
// independently of selected class and without granting SELECT on those columns.
func TestReferenceMergeUpdateMixedDisclosure(t *testing.T) {
	for _, tc := range []struct{ name, grants, on, arms string }{
		{"matchedWithInsertManifest", "GRANT UPDATE(a),INSERT(secret) ON t TO merge_update_actor", "TRUE", "WHEN MATCHED THEN UPDATE SET a=NULL WHEN NOT MATCHED THEN INSERT(secret) VALUES(0)"},
		{"insertWithUpdateManifest", "GRANT INSERT(a),UPDATE(secret) ON t TO merge_update_actor", "FALSE", "WHEN MATCHED AND FALSE THEN UPDATE SET secret=0 WHEN NOT MATCHED THEN INSERT(a) VALUES(NULL)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeUpdateACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				if b.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
				}
				for _, q := range []string{"CREATE USER merge_update_actor", "CREATE TABLE t(id INT,a INT NOT NULL,secret INT DEFAULT 99)", tc.grants} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO t VALUES(1,10,99)", 1))
				actor, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer actor.Close()
				fixture.LockExec(t, p.Ctx, actor, "SET SESSION AUTHORIZATION merge_update_actor", 0)
				defer actor.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON " + tc.on + " " + tc.arms
				step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (a, secret) = (null, 99).", Schema: "public", Table: "t", Column: "a"})
				step.Prepared = prepared
				step.ErrorStage = "execute"
				fixture.InsertSelectRun(t, p.Ctx, actor, b.Recorder, step)
				run(fixture.Select("SELECT * FROM t", []string{"id", "a", "secret"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(99)}}))
			})
		})
	}
}
