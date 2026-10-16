package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantJSONBConcatDeleteAuthority(t *testing.T) {
	// The three admitted operator signatures are distinct EXECUTE targets:
	// ||(3802,3802) -> jsonb_concat3301; -(3802,25) -> jsonb_delete3302;
	// -(3802,23) -> jsonb_delete3303. TEXT[] deletion is not admitted.
	for _, tc := range []struct {
		label, name, op, rhs, formal, literal, want, empty string
	}{
		{"concat", "jsonb_concat", "||", "k", "JSONB", "'[3]'::JSONB", `[1, "x", "x", 2, 3]`, `[]`},
		{"delete_text", "jsonb_delete", "-", "key", "TEXT", "'x'::TEXT", `[1, 2]`, `[]`},
		{"delete_int4", "jsonb_delete", "-", "idx", "INTEGER", "-1::INTEGER", `[1, "x", "x"]`, `[]`},
	} {
		t.Run(tc.label, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_concat_delete(id INT,j JSONB,k JSONB,key TEXT,idx INT)", 0))
				run(fixture.Exec(`INSERT INTO og_concat_delete VALUES
					(1,'[1,"x","x",2]','[3]','x',-1),
					(2,NULL,'[3]','x',-1),
					(3,'[1,"x","x",2]',NULL,NULL,NULL),
					(4,'[]','[]','missing',99),
					(5,'{"a":1,"b":1}','{"a":2,"c":3}','a',0)`, 5))
				run(fixture.Exec("GRANT SELECT ON og_concat_delete TO og_object_reader", 0))
				call := "j " + tc.op + " " + tc.rhs
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_concat_delete"+tail, []string{"v"}, []string{"JSONB"}, [][]any{{want}}, args...)
				}
				run(fixture.Exec("CREATE VIEW og_concat_delete_view AS SELECT "+call+" AS v FROM og_concat_delete WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_concat_delete_view TO og_object_reader", 0))
				cases := []objectGrantProviderCase{
					{value(call, " WHERE id=1", tc.want), true, true},
					{value("j OPERATOR(pg_catalog."+tc.op+") "+tc.rhs, " WHERE id=1", tc.want), true, true},
					{fixture.Select("SELECT v FROM og_concat_delete_view", []string{"v"}, []string{"JSONB"}, [][]any{{tc.want}}), true, true},
					{value("$1::JSONB "+tc.op+" "+tc.rhs, " WHERE id=1", tc.want, `[1,"x","x",2]`), true, true},
					{value("CASE WHEN id=1 THEN "+call+" ELSE NULL::JSONB END", " WHERE id=1", tc.want), true, true},
					// A runtime NULL does not remove initialization demand.
					{value(call, " WHERE id=2", nil), true, true},
					{value(call, " WHERE id=3", nil), true, true},
					// Empty arrays, missing keys and out-of-range indices still call.
					{value(call, " WHERE id=4", tc.empty), true, true},
					{value("NULL::JSONB "+tc.op+" "+tc.rhs, " WHERE id=1", nil), false, true},
					{value("j "+tc.op+" NULL::"+tc.formal, " WHERE id=1", nil), false, true},
					// Fresh/custom parameter NULL, not a generic-plan guarantee.
					{value("$1::JSONB "+tc.op+" "+tc.rhs, " WHERE id=1", nil, nil), false, false},
					{value("j "+tc.op+" $1::"+tc.formal, " WHERE id=1", nil, nil), false, false},
					{value("CASE WHEN FALSE THEN "+call+" ELSE NULL::JSONB END", " WHERE id=1", nil), false, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_concat_delete WHERE id>99", []string{"v"}, []string{"JSONB"}, nil), true, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_concat_delete WHERE FALSE", []string{"v"}, []string{"JSONB"}, nil), true, true},
					// Immutable constant plans may fold in PostgreSQL: fresh only.
					{fixture.Select(`SELECT '[1,"x","x",2]'::JSONB `+tc.op+" "+tc.literal+" AS v", []string{"v"}, []string{"JSONB"}, [][]any{{tc.want}}), true, false},
				}
				var rhsArg any
				switch tc.label {
				case "concat":
					rhsArg = `[3]`
					cases = append(cases, objectGrantProviderCase{value(call, " WHERE id=5", `{"a": 2, "b": 1, "c": 3}`), true, true})
				case "delete_text":
					rhsArg = "x"
					cases = append(cases, objectGrantProviderCase{value(call, " WHERE id=5", `{"b": 1}`), true, true})
				case "delete_int4":
					rhsArg = int64(-1)
					// SMALLINT is coerced to the one INT4 formal, not a new overload.
					cases = append(cases, objectGrantProviderCase{value("j - idx::SMALLINT", " WHERE id=1", tc.want), true, true})
				}
				cases = append(cases, objectGrantProviderCase{value("j "+tc.op+" $1::"+tc.formal, " WHERE id=1", tc.want, rhsArg), true, true})
				// The other admitted signatures retain their independent ACLs,
				// including the same-name jsonb_delete overload sibling.
				var controls []fixture.Step
				if tc.label != "concat" {
					controls = append(controls, value("j || k", " WHERE id=1", `[1, "x", "x", 2, 3]`))
				}
				if tc.label != "delete_text" {
					controls = append(controls, value("j - key", " WHERE id=1", `[1, 2]`))
				}
				if tc.label != "delete_int4" {
					controls = append(controls, value("j - idx", " WHERE id=1", `[1, "x", "x"]`))
				}
				target := "pg_catalog." + tc.name + "(jsonb," + tc.formal + ")"
				objectGrantProviderLifecycle(t, p, b, c, run, target, "function", tc.name, cases, controls)
			})
		})
	}
}
