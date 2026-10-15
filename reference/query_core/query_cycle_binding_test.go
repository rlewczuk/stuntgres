package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryCycleBinding(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			for _, tc := range []struct {
				name, q        string
				columns, types []string
				rows           [][]any
			}{
				{"unqualified self star", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT * FROM r) CYCLE n SET cyc USING path SELECT * FROM r", []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), false, "{(1)}"}, {int64(1), true, "{(1),(1)}"}}},
				{"qualified self star", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.* FROM r) CYCLE n SET cyc USING path SELECT r.* FROM r", []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), false, "{(1)}"}, {int64(1), true, "{(1),(1)}"}}},
				{"explicit generated columns", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE NOT cyc AND cardinality(path)<3) CYCLE n SET cyc USING path SELECT * FROM r", []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), false, "{(1)}"}, {int64(2), false, "{(1),(2)}"}, {int64(3), false, "{(1),(2),(3)}"}}},
				{"self alias prefix", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT t.x FROM r AS t(x) WHERE NOT t.cyc) CYCLE n SET cyc USING path SELECT * FROM r", []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), false, "{(1)}"}, {int64(1), true, "{(1),(1)}"}}},
				{"original alias prefix", "WITH RECURSIVE r(x) AS (SELECT 1 AS old,2 AS y UNION ALL SELECT x,y FROM r) CYCLE x,y SET cyc USING path SELECT * FROM r", []string{"x", "y", "cyc", "path"}, []string{"INT4", "INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), int64(2), false, `{"(1,2)"}`}, {int64(1), int64(2), true, `{"(1,2)","(1,2)"}`}}},
				{"quoted names", `WITH RECURSIVE "R"("Key") AS (SELECT 'a'::text UNION ALL SELECT "Key" FROM "R") CYCLE "Key" SET "Mark" TO 'Y' DEFAULT 'N' USING "Path" SELECT * FROM "R"`, []string{"Key", "Mark", "Path"}, []string{"TEXT", "TEXT", "_RECORD"}, [][]any{{"a", "N", "{(a)}"}, {"a", "Y", "{(a),(a)}"}}},
				{"siblings and unused valid cycle", "WITH RECURSIVE sibling(x) AS (SELECT 7), r(n) AS (SELECT x FROM sibling UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path, unused(n) AS (SELECT 9 UNION ALL SELECT n FROM unused) CYCLE n SET cyc USING path SELECT * FROM r", []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{int64(7), false, "{(7)}"}, {int64(7), true, "{(7),(7)}"}}},
				{"contained scalar star", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT (SELECT r.*) FROM r) CYCLE n SET cyc USING path SELECT * FROM r", []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), false, "{(1)}"}, {int64(1), true, "{(1),(1)}"}}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					subqueryBoth(subqueryRun(t, p, backend, c), cteBag(tc.q, tc.columns, tc.types, tc.rows))
				})
			}
			for _, hint := range []string{"MATERIALIZED", "NOT MATERIALIZED"} {
				t.Run(hint, func(t *testing.T) {
					q := "WITH RECURSIVE r(n) AS " + hint + " (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path SELECT * FROM r"
					subqueryBoth(subqueryRun(t, p, backend, c), cteBag(q, []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), false, "{(1)}"}, {int64(1), true, "{(1),(1)}"}}))
				})
			}
			{
				base := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path"
				for _, tc := range []struct {
					name, suffix   string
					columns, types []string
					rows           [][]any
				}{
					{"contained sibling star", ", s AS (SELECT * FROM r) SELECT * FROM s", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}},
					{"contained sibling explicit generated", ", s AS (SELECT n,cyc,path::text AS p FROM r) SELECT * FROM s", []string{"n", "cyc", "p"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(1), false, "{(1)}"}, {int64(1), true, "{(1),(1)}"}}},
					{"external original alias prefix", " SELECT * FROM r AS t(x)", []string{"x", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), false, "{(1)}"}, {int64(1), true, "{(1),(1)}"}}},
				} {
					t.Run(tc.name, func(t *testing.T) {
						subqueryBoth(subqueryRun(t, p, backend, c), cteBag(base+tc.suffix, tc.columns, tc.types, tc.rows))
					})
				}
				t.Run("external aliases cannot consume generated", func(t *testing.T) {
					q := base + " SELECT * FROM r AS t(x,y,z)"
					cycleAssertDiagnostic(t, p, backend, c, q, cycleDiagnostic("42P10", `table "t" has 1 columns available but 3 columns specified`, q, ""))
				})
			}
		})
	}
}
