package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantAnalysisOnlyDemand(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE admission_input(id INT,n INT)", 0))
		run(fixture.Exec("INSERT INTO admission_input VALUES(1,-7),(2,-3)", 2))
		run(fixture.Exec("GRANT SELECT ON admission_input TO og_object_reader", 0))
		one := func(q string) fixture.Step {
			return fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
		}
		pairs := func(q string, absolute bool) fixture.Step {
			rows := [][]any{{int64(1), int64(-7)}, {int64(2), int64(-3)}}
			if absolute {
				rows = [][]any{{int64(1), int64(7)}, {int64(2), int64(3)}}
			}
			return fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
		}
		cases := []objectGrantProviderCase{
			{one("WITH unused AS (SELECT pg_catalog.abs(-7::integer)) SELECT 1 AS v"), false, true},
			{fixture.Select("SELECT id FROM admission_input WINDOW unused AS (ORDER BY pg_catalog.abs(n)) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}), true, true},
			{pairs("WITH outer_c AS (WITH unused AS (SELECT pg_catalog.abs(n) FROM admission_input) SELECT id,n AS v FROM admission_input) SELECT id,v FROM outer_c ORDER BY id", false), false, true},
			{pairs("SELECT id,pg_catalog.abs(n) AS v FROM admission_input ORDER BY id", true), true, true},
			{pairs("WITH c AS (SELECT id,pg_catalog.abs(n) AS v FROM admission_input) SELECT id,v FROM c ORDER BY id", true), true, true},
			{pairs("SELECT id,(SELECT pg_catalog.abs(n)) AS v FROM admission_input ORDER BY id", true), true, true},
			{fixture.Select("SELECT id,row_number() OVER used AS rn FROM admission_input WINDOW used AS (ORDER BY pg_catalog.abs(n)) ORDER BY id", []string{"id", "rn"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}), true, true},
		}
		for _, materialization := range []string{"", "MATERIALIZED ", "NOT MATERIALIZED "} {
			q := "WITH c AS " + materialization + "(SELECT id,CASE WHEN FALSE THEN pg_catalog.abs(n) ELSE n END AS v FROM admission_input) SELECT id,v FROM c ORDER BY id"
			cases = append(cases, objectGrantProviderCase{pairs(q, false), false, true})
		}
		for _, predicate := range []string{"n>99", "FALSE"} {
			q := "SELECT pg_catalog.abs(n) AS v FROM admission_input WHERE " + predicate
			cases = append(cases, objectGrantProviderCase{fixture.Select(q, []string{"v"}, []string{"INT4"}, nil), true, true})
		}
		objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.abs(integer)", "function", "abs", cases, nil)
	})
}
