package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantBlockingInquiryAuthority(t *testing.T) {
	for _, name := range []string{"pg_blocking_pids", "pg_safe_snapshot_blocking_pids"} {
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_blocking_input(id INT,pid INT)", 0))
				run(fixture.Exec("INSERT INTO og_blocking_input VALUES(1,2147483647),(2,NULL)", 2))
				run(fixture.Exec("GRANT SELECT ON og_blocking_input TO og_object_reader", 0))
				call := "pg_catalog." + name + "(pid)"
				run(fixture.Exec("CREATE VIEW og_blocking_view AS SELECT "+call+" AS v FROM og_blocking_input WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_blocking_view TO og_object_reader", 0))
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_blocking_input"+tail, []string{"v"}, []string{"_INT4"}, [][]any{{want}}, args...)
				}
				cases := []objectGrantProviderCase{
					{value(call, " WHERE id=1", "{}"), true, true},
					{value(call, " WHERE id=2", nil), true, true},
					{value(name+"(2147483647)", " WHERE id=1", "{}"), true, true},
					{value("pg_catalog."+name+"($1::INTEGER)", " WHERE id=1", "{}", int64(2147483647)), true, true},
					{value("pg_catalog."+name+"($1::INTEGER)", " WHERE id=1", nil, nil), false, false},
					{value(name+"(NULL::INTEGER)", " WHERE id=1", nil), false, true},
					{value("CASE WHEN FALSE THEN "+call+" ELSE NULL::INTEGER[] END", " WHERE id=1", nil), false, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_blocking_input WHERE id>99", []string{"v"}, []string{"_INT4"}, nil), true, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_blocking_input WHERE FALSE", []string{"v"}, []string{"_INT4"}, nil), true, true},
					{fixture.Select("SELECT v FROM og_blocking_view", []string{"v"}, []string{"_INT4"}, [][]any{{"{}"}}), true, true},
				}
				sibling := "pg_safe_snapshot_blocking_pids"
				if name == sibling {
					sibling = "pg_blocking_pids"
				}
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+name+"(integer)", "function", name, cases, []fixture.Step{value(sibling+"(pid)", " WHERE id=1", "{}")})
			})
		})
	}
}
