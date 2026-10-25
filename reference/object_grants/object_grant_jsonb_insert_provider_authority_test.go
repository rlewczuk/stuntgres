package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantJSONBInsertProviderAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_jsonb_insert_provider(id INT,j JSONB,p TEXT[],replacement JSONB,a BOOLEAN)", 0))
		run(fixture.Exec(`INSERT INTO og_jsonb_insert_provider VALUES
			(1,'[0,1,2]','{1}','"x"',false),
			(2,NULL,'{1}','"x"',false),
			(3,'[0,1,2]',NULL,'"x"',false),
			(4,'[0,1,2]','{1}',NULL,false),
			(5,'[0,1,2]','{1}','"x"',NULL)`, 5))
		run(fixture.Exec("GRANT SELECT ON og_jsonb_insert_provider TO og_object_reader", 0))
		selectValue := func(expr, tail string, want any, args ...any) fixture.Step {
			return fixture.Select("SELECT "+expr+" AS v FROM og_jsonb_insert_provider"+tail, []string{"v"}, []string{"JSONB"}, [][]any{{want}}, args...)
		}
		before, after := `[0, "x", 1, 2]`, `[0, 1, "x", 2]`
		call := "jsonb_insert(j,p,replacement)"
		full := "jsonb_insert(j,p,replacement,a)"
		cases := []objectGrantProviderCase{
			// Default, explicit false, row-backed false and explicit true all
			// select the same four-formal function, with different values.
			{selectValue(call, " WHERE id=1", before), true, true},
			{selectValue("jsonb_insert(j,p,replacement,false)", " WHERE id=1", before), true, true},
			{selectValue(full, " WHERE id=1", before), true, true},
			{selectValue("jsonb_insert(j,p,replacement,true)", " WHERE id=1", after), true, true},
			{selectValue("pg_catalog.jsonb_insert(j,p,replacement)", " WHERE id=1", before), true, true},
			{selectValue("jsonb_insert(replacement=>replacement,path=>p,jsonb_in=>j)", " WHERE id=1", before), true, true},
			{selectValue("jsonb_insert(insert_after=>a,replacement=>replacement,path=>p,jsonb_in=>j)", " WHERE id=1", before), true, true},
			{selectValue("jsonb_insert(insert_after=>true,replacement=>replacement,path=>p,jsonb_in=>j)", " WHERE id=1", after), true, true},
			{selectValue("jsonb_insert(j,p,replacement,insert_after=>true)", " WHERE id=1", after), true, true},
			// The omitted fourth argument is false, not the nullable a column.
			{selectValue(call, " WHERE id=5", before), true, true},

			// Non-NULL parameters retain current authority on held statements.
			{selectValue("jsonb_insert($1::JSONB,p,replacement)", " WHERE id=1", before, "[0,1,2]"), true, true},
			{selectValue("jsonb_insert(j,$1::TEXT[],replacement)", " WHERE id=1", before, "{1}"), true, true},
			{selectValue("jsonb_insert(j,p,$1::JSONB)", " WHERE id=1", before, `"x"`), true, true},
			{selectValue("jsonb_insert(j,p,replacement,$1::BOOLEAN)", " WHERE id=1", before, false), true, true},
			{selectValue("jsonb_insert(j,p,replacement,$1::BOOLEAN)", " WHERE id=1", after, true), true, true},

			// A statically known strict NULL can eliminate the call. These
			// parameter-NULL cases deliberately do not assert generic-plan behavior.
			{selectValue("jsonb_insert(NULL::JSONB,p,replacement)", " WHERE id=1", nil), false, true},
			{selectValue("jsonb_insert(j,NULL::TEXT[],replacement)", " WHERE id=1", nil), false, true},
			{selectValue("jsonb_insert(j,p,NULL::JSONB)", " WHERE id=1", nil), false, true},
			{selectValue("jsonb_insert(j,p,replacement,NULL::BOOLEAN)", " WHERE id=1", nil), false, true},
			{selectValue("jsonb_insert($1::JSONB,p,replacement)", " WHERE id=1", nil, nil), false, false},
			{selectValue("jsonb_insert(j,$1::TEXT[],replacement)", " WHERE id=1", nil, nil), false, false},
			{selectValue("jsonb_insert(j,p,$1::JSONB)", " WHERE id=1", nil, nil), false, false},
			{selectValue("jsonb_insert(j,p,replacement,$1::BOOLEAN)", " WHERE id=1", nil, nil), false, false},

			// A dead arm is removed, but a surviving row-dependent call still
			// demands authority even when its source produces no rows.
			{selectValue("CASE WHEN FALSE THEN "+call+" ELSE NULL END", " WHERE id=1", nil), false, true},
			{fixture.Select("SELECT "+call+" AS v FROM og_jsonb_insert_provider WHERE id>99", []string{"v"}, []string{"JSONB"}, nil), true, true},
			{fixture.Select("SELECT "+call+" AS v FROM og_jsonb_insert_provider WHERE FALSE", []string{"v"}, []string{"JSONB"}, nil), true, true},
		}
		// Each full-formal operand is independently NULL only at row evaluation.
		for _, tail := range []string{" WHERE id=2", " WHERE id=3", " WHERE id=4", " WHERE id=5"} {
			cases = append(cases, objectGrantProviderCase{selectValue(full, tail, nil), true, true})
		}
		controls := []fixture.Step{
			selectValue("jsonb_set(j,p,replacement,a)", " WHERE id=1", `[0, "x", 2]`),
		}
		objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.jsonb_insert(jsonb,text[],jsonb,boolean)", "function", "jsonb_insert", cases, controls)
	})
}
