package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainJSONNonExecution(t *testing.T) {
	t.Run("logical-properties", explainJSONLogicalProperties)
	t.Run("command-properties", explainJSONCommandProperties)
	t.Run("security-and-locator", explainJSONSecurityLocator)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			explainJSONSetup(t, p, b)
			for _, query := range []string{"CREATE SEQUENCE explain_json_s START 1", "CREATE TABLE explain_json_default(id integer DEFAULT nextval('explain_json_s'))"} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(query, 0))
			}
			unchanged := func() {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id FROM explain_json_default", []string{"id"}, []string{"INT4"}, nil))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT last_value,is_called FROM explain_json_s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,v FROM explain_json ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
			}
			for _, query := range []string{
				"INSERT INTO explain_json_default DEFAULT VALUES RETURNING id",
				"SELECT nextval('explain_json_s')",
				"INSERT INTO explain_json_default VALUES(99) RETURNING nextval('explain_json_s')",
				"WITH w AS (INSERT INTO explain_json_default DEFAULT VALUES RETURNING id) SELECT * FROM w",
				"SELECT nextval('explain_json_s'), generate_series(1,3)",
				"SELECT * FROM generate_series(1,nextval('explain_json_s'))",
				"UPDATE explain_json SET v='changed' WHERE id=2 RETURNING nextval('explain_json_s')",
				"DELETE FROM explain_json WHERE id=1 RETURNING nextval('explain_json_s')",
				"MERGE INTO explain_json t USING (VALUES(2,'changed'),(4,'four')) s(id,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v) RETURNING nextval('explain_json_s')",
				"WITH w AS (UPDATE explain_json SET v='changed' RETURNING id) SELECT * FROM w",
				"WITH w AS (DELETE FROM explain_json RETURNING id) SELECT * FROM w",
			} {
				explainJSONRoutes(t, p, b, "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) "+query, func(document explainJSONDocument, _ string) { explainJSONOrdinary(t, b, document); unchanged() })
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO explain_json_default DEFAULT VALUES", 1))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id FROM explain_json_default", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT nextval('explain_json_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
	}
}
