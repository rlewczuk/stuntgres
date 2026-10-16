package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantMetaphoneProviderAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, target, value, sibling, siblingValue string
	}{
		{"metaphone", "public.metaphone(text,integer)", "SM0", "dmetaphone_alt", "XMT"},
		{"dmetaphone", "public.dmetaphone(text)", "SM0", "dmetaphone_alt", "XMT"},
		{"dmetaphone_alt", "public.dmetaphone_alt(text)", "XMT", "dmetaphone", "SM0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				// Reuse public, which the shared PostgreSQL fixture resets.
				// Native owns a fresh engine and does not need an invented
				// DROP EXTENSION implementation. PostgreSQL owns a real
				// database-wide extension, even if an earlier case moved it.
				if b.PG {
					clean := func() {
						for _, q := range []string{
							"ROLLBACK",
							"RESET SESSION AUTHORIZATION",
							"DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE",
						} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error(q, err)
							}
						}
					}
					defer clean()
					clean()
				}
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA public", 0))
				run(fixture.Exec("CREATE TABLE og_metaphone_source(id INT,source TEXT)", 0))
				run(fixture.Exec("INSERT INTO og_metaphone_source VALUES(1,'Smith'),(2,NULL),(3,'')", 3))
				run(fixture.Exec("GRANT SELECT ON og_metaphone_source TO og_object_reader", 0))

				call := func(source, limit string) string {
					if tc.name == "metaphone" {
						return "public.metaphone(" + source + "," + limit + ")"
					}
					return "public." + tc.name + "(" + source + ")"
				}
				selectValue := func(expr, tail string, value any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_metaphone_source"+tail, []string{"v"}, []string{"TEXT"}, [][]any{{value}}, args...)
				}
				expr := call("source", "8::INTEGER")
				parameter := call("$1::TEXT", "8::INTEGER")
				run(fixture.Exec("CREATE VIEW og_metaphone_view AS SELECT "+expr+" AS v FROM og_metaphone_source WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_metaphone_view TO og_object_reader", 0))
				cases := []objectGrantProviderCase{
					// The same held row-backed statement must follow current
					// EXECUTE through revoke/grant/revoke, not cached authority.
					{selectValue(expr, " WHERE id=1", tc.value), true, true},
					{fixture.Select("SELECT v FROM og_metaphone_view", []string{"v"}, []string{"TEXT"}, [][]any{{tc.value}}), true, true},
					// Runtime NULL/empty values retain initialization demand.
					{selectValue(expr, " WHERE id=2", nil), true, true},
					{selectValue(expr, " WHERE id=3", ""), true, true},
					{selectValue(parameter, " WHERE id=1", tc.value, "Smith"), true, true},
					// Literal strict NULL removes the selected invocation.
					{selectValue(call("NULL::TEXT", "8::INTEGER"), " WHERE id=1", nil), false, true},
					// Known NULL binds are fresh/custom, not held generic plans.
					{selectValue(parameter, " WHERE id=1", nil, nil), false, false},
					{selectValue("CASE WHEN FALSE THEN "+expr+" ELSE NULL::TEXT END", " WHERE id=1", nil), false, true},
					// Nonconstant calls initialize even over empty input.
					{fixture.Select("SELECT "+expr+" AS v FROM og_metaphone_source WHERE id>99", []string{"v"}, []string{"TEXT"}, nil), true, true},
					{fixture.Select("SELECT "+expr+" AS v FROM og_metaphone_source WHERE FALSE", []string{"v"}, []string{"TEXT"}, nil), true, true},
				}
				if tc.name == "metaphone" {
					// Exercise the actual second INT4 formal, not a one-argument
					// stand-in or a TEXT,TEXT identity.
					cases = append(cases,
						objectGrantProviderCase{selectValue(call("source", "$1::INTEGER"), " WHERE id=1", tc.value, int64(8)), true, true},
						// A known NULL limit also eliminates this strict call.
						objectGrantProviderCase{selectValue(call("source", "NULL::INTEGER"), " WHERE id=1", nil), false, true},
						objectGrantProviderCase{selectValue(call("source", "$1::INTEGER"), " WHERE id=1", nil, nil), false, false},
					)
				}
				controls := []fixture.Step{
					// A genuinely different installed routine remains usable
					// while only the selected target loses EXECUTE.
					fixture.Select("SELECT public."+tc.sibling+"(source) AS v FROM og_metaphone_source WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{tc.siblingValue}}),
				}
				objectGrantProviderLifecycle(t, p, b, c, run, tc.target, "function", tc.name, cases, controls)
			})
		})
	}
}
