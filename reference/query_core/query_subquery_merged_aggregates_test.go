package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQuerySubqueryMergedAggregateOwnership(t *testing.T) {
	cases := []struct {
		name, q        string
		columns, types []string
		rows           [][]any
	}{
		{"outer", "SELECT (SELECT avg(o.id)) AS n,(SELECT avg(o.f)) AS f,(SELECT avg(o.iv)) AS iv,(SELECT min(o.id)) AS lo,(SELECT max(o.id)) AS hi FROM merged_outer o", []string{"n", "f", "iv", "lo", "hi"}, []string{"NUMERIC", "FLOAT8", "INTERVAL", "INT4", "INT4"}, [][]any{{"2.0000000000000000", float64(2), "2 days", int64(1), int64(3)}}},
		{"separator", "SELECT (SELECT string_agg('x',o.sep)) AS s FROM merged_outer o", []string{"s"}, []string{"TEXT"}, [][]any{{"x|x|x"}}},
		{"order", "SELECT (SELECT string_agg('x','|' ORDER BY o.id)) AS s FROM merged_outer o", []string{"s"}, []string{"TEXT"}, [][]any{{"x|x|x"}}},
		{"grandparent", "SELECT (SELECT (SELECT avg(o.id))) AS n FROM merged_outer o", []string{"n"}, []string{"NUMERIC"}, [][]any{{"2.0000000000000000"}}},
		{"sublink-argument", "SELECT avg((SELECT o.id)) AS n FROM merged_outer o", []string{"n"}, []string{"NUMERIC"}, [][]any{{"2.0000000000000000"}}},
		{"child-local", "SELECT o.id,(SELECT avg(i.id) FROM merged_inner i) AS n FROM merged_outer o ORDER BY o.id", []string{"id", "n"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(1), "15.0000000000000000"}, {int64(2), "15.0000000000000000"}, {int64(3), "15.0000000000000000"}}},
		{"legal-nesting", "SELECT avg((SELECT min(i.id) FROM merged_inner i)) AS n FROM merged_outer o", []string{"n"}, []string{"NUMERIC"}, [][]any{{"10.0000000000000000"}}},
		{"group-rewrite", "SELECT o.sep,(SELECT string_agg(o.label,o.sep ORDER BY o.id)) AS s FROM merged_outer o GROUP BY o.sep ORDER BY o.sep", []string{"sep", "s"}, []string{"TEXT", "TEXT"}, [][]any{{"|", "a|b|c"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(fixture.Exec("SET IntervalStyle='postgres'", 0))
				run(fixture.Exec("CREATE TABLE merged_outer(id INT,f DOUBLE PRECISION,iv INTERVAL,sep TEXT,label TEXT)", 0))
				run(fixture.Exec("INSERT INTO merged_outer VALUES(1,1,'1 day','|','a'),(2,2,'2 days','|','b'),(3,3,'3 days','|','c')", 3))
				run(fixture.Exec("CREATE TABLE merged_inner(id INT)", 0))
				run(fixture.Exec("INSERT INTO merged_inner VALUES(10),(20)", 2))
				subqueryBoth(run, fixture.Select(tc.q, tc.columns, tc.types, tc.rows))
				run(fixture.Exec("CREATE VIEW merged_aggregate AS "+tc.q, 0))
				fixture.ViewCopy(t, p, b, "merged_aggregate", "merged_aggregate_copy")
				check := func() {
					for _, name := range []string{"merged_aggregate", "merged_aggregate_copy"} {
						s := fixture.Select("SELECT * FROM "+name, tc.columns, tc.types, tc.rows)
						s.Bag = true
						subqueryBoth(run, s)
					}
				}
				check()
				if !b.PG {
					dump := fixture.ViewDump(t, p)
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					fixture.ViewRestore(t, p, dump)
					check()
				}
			})
		})
	}
}

func TestReferenceQuerySubqueryMergedAggregateNesting(t *testing.T) {
	for _, q := range []string{
		"SELECT avg((SELECT min(o.id))) FROM merged_outer o",
		"SELECT string_agg('x',(SELECT min(o.sep))) FROM merged_outer o",
		"SELECT string_agg('x','|' ORDER BY (SELECT min(o.id))) FROM merged_outer o",
	} {
		t.Run(q, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE merged_outer(id INT,sep TEXT)", 0))
				want := fixture.ControlsReferenceDiagnostic("42803", "aggregate function calls cannot be nested")
				want.Position = fixture.GeneratedStoredPosition(q, "min")
				subqueryAnalysisFailure(run, q, want)
			})
		})
	}
}
