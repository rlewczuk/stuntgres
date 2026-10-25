package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantLevenshteinProviderAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, target   string
		costs, bounded bool
	}{
		{"levenshtein", "public.levenshtein(text,text)", false, false},
		{"levenshtein", "public.levenshtein(text,text,integer,integer,integer)", true, false},
		{"levenshtein_less_equal", "public.levenshtein_less_equal(text,text,integer)", false, true},
		{"levenshtein_less_equal", "public.levenshtein_less_equal(text,text,integer,integer,integer,integer)", true, true},
	} {
		t.Run(tc.target, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				if b.PG {
					clean := func() {
						for _, query := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE"} {
							if _, err := c.ExecContext(p.Ctx, query); err != nil {
								t.Error(query, err)
							}
						}
					}
					defer clean()
					clean()
				}
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA public", 0))
				run(fixture.Exec("CREATE TABLE og_levenshtein_source(id INT,source TEXT)", 0))
				run(fixture.Exec("INSERT INTO og_levenshtein_source VALUES(1,'kitten'),(2,NULL),(3,'sitting')", 3))
				run(fixture.Exec("GRANT SELECT ON og_levenshtein_source TO og_object_reader", 0))
				call := func(source string) string {
					tail := ",'sitting'::TEXT"
					if tc.costs {
						tail += ",1,1,1"
					}
					if tc.bounded {
						tail += ",8"
					}
					return "public." + tc.name + "(" + source + tail + ")"
				}
				selectValue := func(expr, tail string, value any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_levenshtein_source"+tail, []string{"v"}, []string{"INT4"}, [][]any{{value}}, args...)
				}
				expr, parameter := call("source"), call("$1::TEXT")
				run(fixture.Exec("CREATE VIEW og_levenshtein_view AS SELECT "+expr+" AS v FROM og_levenshtein_source WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_levenshtein_view TO og_object_reader", 0))
				cases := []objectGrantProviderCase{
					{selectValue(expr, " WHERE id=1", int64(3)), true, true},
					{fixture.Select("SELECT v FROM og_levenshtein_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}), true, true},
					{selectValue(expr, " WHERE id=2", nil), true, true},
					{selectValue(expr, " WHERE id=3", int64(0)), true, true},
					{selectValue(parameter, " WHERE id=1", int64(3), "kitten"), true, true},
					{selectValue(parameter, " WHERE id=1", nil, nil), false, false},
					{selectValue(call("NULL::TEXT"), " WHERE id=1", nil), false, true},
					{selectValue("CASE WHEN FALSE THEN "+expr+" ELSE 7 END", " WHERE id=1", int64(7)), false, true},
					{fixture.Select("SELECT "+expr+" AS v FROM og_levenshtein_source WHERE id>99", []string{"v"}, []string{"INT4"}, nil), true, true},
					{fixture.Select("SELECT "+expr+" AS v FROM og_levenshtein_source WHERE FALSE", []string{"v"}, []string{"INT4"}, nil), true, true},
				}
				control := fixture.Select("SELECT public.daitch_mokotoff('A') AS v", []string{"v"}, []string{"_TEXT"}, [][]any{{"{000000}"}})
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, tc.target, "function", tc.name, cases, []fixture.Step{control})
			})
		})
	}
}
