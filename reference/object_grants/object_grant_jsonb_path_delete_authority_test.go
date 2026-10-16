package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantJSONBPathDeleteProviderAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_jsonb_delete_source(id INT,j JSONB,p TEXT[])", 0))
		run(fixture.Exec(`INSERT INTO og_jsonb_delete_source VALUES
			(1,'{"a":[10,20],"keep":3}','{a,-1}'),
			(2,NULL,'{a,-1}'),
			(3,'{"a":[10,20],"keep":3}',NULL),
			(4,'{"a":[10,20],"keep":3}','{}'),
			(5,'{"a":[10,20],"keep":3}','{missing,NULL}')`, 5))
		run(fixture.Exec("GRANT SELECT ON og_jsonb_delete_source TO og_object_reader", 0))
		selectValue := func(expr, tail string, want any, args ...any) fixture.Step {
			return fixture.Select("SELECT "+expr+" AS v FROM og_jsonb_delete_source"+tail, []string{"v"}, []string{"JSONB"}, [][]any{{want}}, args...)
		}
		call := "j #- p"
		deleted, original := `{"a": [10], "keep": 3}`, `{"a": [10, 20], "keep": 3}`
		run(fixture.Exec("CREATE VIEW og_jsonb_delete_view AS SELECT "+call+" AS v FROM og_jsonb_delete_source WHERE id=1", 0))
		run(fixture.Exec("GRANT SELECT ON og_jsonb_delete_view TO og_object_reader", 0))
		cases := []objectGrantProviderCase{
			// Both spellings select procedure 3304 with full JSONB,TEXT[]
			// formals. Held direct and stored consumers use current EXECUTE.
			{selectValue(call, " WHERE id=1", deleted), true, true},
			{selectValue("j OPERATOR(pg_catalog.#-) p", " WHERE id=1", deleted), true, true},
			{fixture.Select("SELECT v FROM og_jsonb_delete_view", []string{"v"}, []string{"JSONB"}, [][]any{{deleted}}), true, true},
			{selectValue("$1::JSONB #- p", " WHERE id=1", deleted, `{"a":[10,20],"keep":3}`), true, true},
			{selectValue("j #- $1::TEXT[]", " WHERE id=1", deleted, "{a,-1}"), true, true},

			// Runtime NULL rows retain initialization demand, unlike a
			// statically known whole strict NULL operand.
			{selectValue(call, " WHERE id=2", nil), true, true},
			{selectValue(call, " WHERE id=3", nil), true, true},
			{selectValue("NULL::JSONB #- p", " WHERE id=1", nil), false, true},
			{selectValue("j #- NULL::TEXT[]", " WHERE id=1", nil), false, true},
			// Fresh/custom parameter NULL, not a generic-plan promise.
			{selectValue("$1::JSONB #- p", " WHERE id=1", nil, nil), false, false},
			{selectValue("j #- $1::TEXT[]", " WHERE id=1", nil, nil), false, false},

			// Empty non-NULL paths and unreached NULL members do not make
			// the whole path NULL or remove the selected invocation.
			{selectValue(call, " WHERE id=4", original), true, true},
			{selectValue(call, " WHERE id=5", original), true, true},
			{selectValue("j #- '{}'::TEXT[]", " WHERE id=1", original), true, true},
			{selectValue("CASE WHEN FALSE THEN "+call+" ELSE NULL::JSONB END", " WHERE id=1", nil), false, true},
			{fixture.Select("SELECT "+call+" AS v FROM og_jsonb_delete_source WHERE id>99", []string{"v"}, []string{"JSONB"}, nil), true, true},
			{fixture.Select("SELECT "+call+" AS v FROM og_jsonb_delete_source WHERE FALSE", []string{"v"}, []string{"JSONB"}, nil), true, true},
		}
		// A separately implemented extraction operator keeps its own ACL.
		controls := []fixture.Step{selectValue("j #> p", " WHERE id=1", "20")}
		objectGrantProviderLifecycle(t, p, b, c, run, "pg_catalog.jsonb_delete_path(jsonb,text[])", "function", "jsonb_delete_path", cases, controls)
	})
}
