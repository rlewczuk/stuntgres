package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantJSONBContainmentAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		want     bool
	}{{"jsonb_contains", "@>", true}, {"jsonb_contained", "<@", false}} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_containment(id INT,j JSONB,k JSONB)", 0))
				run(fixture.Exec(`INSERT INTO og_containment VALUES(1,'{"a":1,"b":2}','{"a":1}'),(2,NULL,'{"a":1}'),(3,'{}','{}')`, 3))
				run(fixture.Exec("GRANT SELECT ON og_containment TO og_object_reader", 0))
				call := "j " + tc.op + " k"
				run(fixture.Exec("CREATE VIEW og_containment_view AS SELECT "+call+" AS v FROM og_containment WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_containment_view TO og_object_reader", 0))
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_containment"+tail, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
				}
				cases := []objectGrantProviderCase{
					{value(call, " WHERE id=1", tc.want), true, true},
					{value("j OPERATOR(pg_catalog."+tc.op+") k", " WHERE id=1", tc.want), true, true},
					{value(call, " WHERE id=2", nil), true, true},
					{value(call, " WHERE id=3", true), true, true},
					{value("$1::JSONB "+tc.op+" k", " WHERE id=1", tc.want, `{"a":1,"b":2}`), true, false},
					{value("$1::JSONB "+tc.op+" k", " WHERE id=1", nil, nil), false, false},
					{value("NULL::JSONB "+tc.op+" k", " WHERE id=1", nil), false, true},
					{value("j "+tc.op+" NULL::JSONB", " WHERE id=1", nil), false, true},
					{value("CASE WHEN FALSE THEN "+call+" ELSE NULL::BOOL END", " WHERE id=1", nil), false, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_containment WHERE id>99", []string{"v"}, []string{"BOOL"}, nil), true, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_containment WHERE FALSE", []string{"v"}, []string{"BOOL"}, nil), true, true},
					{fixture.Select("SELECT v FROM og_containment_view", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}), true, true},
				}
				control := fixture.Select("SELECT j->>'a' AS v FROM og_containment WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"1"}})
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+tc.name+"(jsonb,jsonb)", "function", tc.name, cases, []fixture.Step{control})
			})
		})
	}
}
