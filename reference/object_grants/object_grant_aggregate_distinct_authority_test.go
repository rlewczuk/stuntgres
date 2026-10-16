package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantAggregateDistinctAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.int4eq(integer,integer)", "pg_catalog.texteq(text,text)"})
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_distinct_authority(i INTEGER,s TEXT,sep TEXT)", 0))
		run(fixture.Exec("INSERT INTO og_distinct_authority VALUES(1,'a',','),(1,'a',','),(2,'b',',')", 3))
		run(fixture.Exec("GRANT SELECT ON og_distinct_authority TO og_object_reader", 0))
		count := fixture.Select("SELECT count(DISTINCT i) AS n FROM og_distinct_authority", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}})
		strings := fixture.Select("SELECT string_agg(DISTINCT s,sep ORDER BY s) AS v FROM og_distinct_authority", []string{"v"}, []string{"TEXT"}, [][]any{{"a,b"}})
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(count)
			run(strings)
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.int4eq(INTEGER,INTEGER) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			// One-key aggregate DISTINCT uses support, not caller-checked tuple equality.
			run(count)
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.texteq(TEXT,TEXT) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(count)
			fixture.RoleGrantPermissionDenied(run, strings.Query, "function", "texteq")
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.texteq(TEXT,TEXT) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(count)
			run(strings)
		})
	})
}
