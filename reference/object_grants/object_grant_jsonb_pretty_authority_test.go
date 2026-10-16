package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantJSONBPrettyAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_jsonb_pretty_source(id INT,j JSONB)", 0))
		run(fixture.Exec(`INSERT INTO og_jsonb_pretty_source VALUES(1,'{"a":1,"b":[true,null]}'),(2,NULL),(3,'null')`, 3))
		run(fixture.Exec("GRANT SELECT ON og_jsonb_pretty_source TO og_object_reader", 0))
		run(fixture.Exec("CREATE VIEW og_jsonb_pretty_view AS SELECT jsonb_pretty(j) AS v FROM og_jsonb_pretty_source WHERE id=1", 0))
		run(fixture.Exec("GRANT SELECT ON og_jsonb_pretty_view TO og_object_reader", 0))

		// One independently specified multiline TEXT value witnesses the real
		// provider. Formatting breadth belongs to the existing pretty suite.
		const input = `{"a":1,"b":[true,null]}`
		const pretty = "{\n    \"a\": 1,\n    \"b\": [\n        true,\n        null\n    ]\n}"
		selectValue := func(expr, tail string, want any, args ...any) fixture.Step {
			return fixture.Select("SELECT "+expr+" AS v FROM og_jsonb_pretty_source"+tail, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, args...)
		}
		cases := []objectGrantProviderCase{
			// Genuine row-backed and stored SAME-held calls must recheck the
			// current invoker, not retain the preparing user's authority.
			{selectValue("jsonb_pretty(j)", " WHERE id=1", pretty), true, true},
			{selectValue("pg_catalog.jsonb_pretty(j)", " WHERE id=1", pretty), true, true},
			{fixture.Select("SELECT v FROM og_jsonb_pretty_view", []string{"v"}, []string{"TEXT"}, [][]any{{pretty}}), true, true},
			{selectValue("jsonb_pretty(CASE WHEN id=1 THEN $1::JSONB ELSE j END)", " WHERE id=1", pretty, input), true, true},

			// Runtime SQL NULL retains initialization demand. JSON null is a
			// non-NULL JSONB value whose result is the TEXT string "null".
			{selectValue("jsonb_pretty(j)", " WHERE id=2", nil), true, true},
			{selectValue("jsonb_pretty(j)", " WHERE id=3", "null"), true, true},
			{selectValue("jsonb_pretty(NULL::JSONB)", " WHERE id=1", nil), false, false},
			{selectValue("jsonb_pretty($1::JSONB)", " WHERE id=1", nil, nil), false, false},

			// IMMUTABLE constant-only calls may be folded into a held PG plan;
			// fresh literal/parameter executions are the authority witnesses.
			{selectValue("jsonb_pretty('"+input+"'::JSONB)", " WHERE id=1", pretty), true, false},
			{selectValue("jsonb_pretty($1::JSONB)", " WHERE id=1", pretty, input), true, false},
			{selectValue("CASE WHEN FALSE THEN jsonb_pretty(j) ELSE NULL::TEXT END", " WHERE id=1", nil), false, true},
			{fixture.Select("SELECT jsonb_pretty(j) AS v FROM og_jsonb_pretty_source WHERE id>99", []string{"v"}, []string{"TEXT"}, nil), true, true},
			{fixture.Select("SELECT jsonb_pretty(j) AS v FROM og_jsonb_pretty_source WHERE FALSE", []string{"v"}, []string{"TEXT"}, nil), true, true},
		}
		controls := []fixture.Step{selectValue("j->>'a'", " WHERE id=1", "1")}
		objectGrantProviderLifecycle(t, p, b, c, run, "pg_catalog.jsonb_pretty(jsonb)", "function", "jsonb_pretty", cases, controls)
	})
}
