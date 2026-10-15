package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryCyclePaths(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			run := subqueryRun(t, p, backend, c)
			cycleSeed(run)
			t.Run("fixed graph all public routes", func(t *testing.T) {
				recursiveCTERoutes(t, p, backend, c, cycleGraphStep(cycleGraph+"SELECT * FROM r"))
				q := strings.Replace(cycleGraph, "SELECT 'a'::text", "SELECT $1::text", 1) + "SELECT * FROM r"
				s := cycleGraphStep(q)
				s.Args = []any{"a"}
				recursiveCTERoutes(t, p, backend, c, s)
			})
			for _, tc := range []struct {
				name, body, keys string
				columns, types   []string
				rows             [][]any
			}{
				{"null record", "SELECT NULL::int UNION ALL SELECT n FROM r", "n", []string{"n", "cyc", "path"}, []string{"INT4", "BOOL", "_RECORD"}, [][]any{{nil, false, "{()}"}, {nil, true, "{(),()}"}}},
				{"ordered multikey null", "SELECT 1,NULL::int UNION ALL SELECT x,CASE WHEN y IS NULL THEN 2 ELSE NULL END FROM r", "x,y", []string{"x", "y", "cyc", "path"}, []string{"INT4", "INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), nil, false, `{"(1,)"}`}, {int64(1), int64(2), false, `{"(1,)","(1,2)"}`}, {int64(1), nil, true, `{"(1,)","(1,2)","(1,)"}`}}},
				{"ordered multikey swap", "SELECT 1,2 UNION ALL SELECT y,x FROM r", "x,y", []string{"x", "y", "cyc", "path"}, []string{"INT4", "INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), int64(2), false, `{"(1,2)"}`}, {int64(2), int64(1), false, `{"(1,2)","(2,1)"}`}, {int64(1), int64(2), true, `{"(1,2)","(2,1)","(1,2)"}`}}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					aliases := "n"
					if len(tc.columns) == 4 {
						aliases = "x,y"
					}
					q := "WITH RECURSIVE r(" + aliases + ") AS (" + tc.body + ") CYCLE " + tc.keys + " SET cyc USING path SELECT * FROM r"
					subqueryBoth(subqueryRun(t, p, backend, c), cteBag(q, tc.columns, tc.types, tc.rows))
				})
			}
			run(fixture.Exec("CREATE TABLE cycle_diamond(src TEXT,dst TEXT)", 0))
			run(fixture.Exec("INSERT INTO cycle_diamond VALUES('c','d'),('a','c'),('b','a'),('b','d'),('a','b'),('a','b')", 6))
			for _, distinct := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					t.Run(map[bool]string{false: "all", true: "distinct"}[distinct]+map[bool]string{false: " self first", true: " edges first"}[reverse], func(t *testing.T) {
						join := "r JOIN cycle_diamond e ON r.n=e.src"
						if reverse {
							join = "cycle_diamond e JOIN r ON r.n=e.src"
						}
						union := "UNION ALL"
						if distinct {
							union = "UNION"
						}
						q := "WITH RECURSIVE r(n) AS (SELECT n FROM (VALUES('a'::text),('a'::text)) seed(n) " + union + " SELECT e.dst FROM " + join + ") CYCLE n SET cyc USING path SELECT * FROM r"
						rows := [][]any{{"a", false, "{(a)}"}, {"b", false, "{(a),(b)}"}, {"c", false, "{(a),(c)}"}, {"d", false, "{(a),(b),(d)}"}, {"d", false, "{(a),(c),(d)}"}, {"a", true, "{(a),(b),(a)}"}}
						if !distinct {
							// Two anchor occurrences; b and its children each have two
							// physical edges per anchor. Preserve exact multiplicity.
							rows = [][]any{{"a", false, "{(a)}"}, {"a", false, "{(a)}"}, {"c", false, "{(a),(c)}"}, {"c", false, "{(a),(c)}"}, {"d", false, "{(a),(c),(d)}"}, {"d", false, "{(a),(c),(d)}"}}
							for range 4 {
								rows = append(rows, []any{"b", false, "{(a),(b)}"}, []any{"d", false, "{(a),(b),(d)}"}, []any{"a", true, "{(a),(b),(a)}"})
							}
						}
						subqueryBoth(subqueryRun(t, p, backend, c), cteBag(q, []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "_RECORD"}, rows))
						// Projecting path away must not erase augmented DISTINCT keys.
						var projected [][]any
						for _, row := range rows {
							projected = append(projected, []any{row[0], row[1]})
						}
						q = strings.Replace(q, "SELECT * FROM r", "SELECT n,cyc FROM r", 1)
						subqueryBoth(subqueryRun(t, p, backend, c), cteBag(q, []string{"n", "cyc"}, []string{"TEXT", "BOOL"}, projected))
					})
				}
			}
			for _, tc := range []struct {
				value          any
				first, closing string
			}{
				{"a,b", `{"(\"a,b\")"}`, `{"(\"a,b\")","(\"a,b\")"}`},
				{`a"b`, `{"(\"a\"\"b\")"}`, `{"(\"a\"\"b\")","(\"a\"\"b\")"}`},
				{`a\b`, `{"(\"a\\\\b\")"}`, `{"(\"a\\\\b\")","(\"a\\\\b\")"}`},
				{"", `{"(\"\")"}`, `{"(\"\")","(\"\")"}`},
				{"NULL", "{(NULL)}", "{(NULL),(NULL)}"},
				{nil, "{()}", "{(),()}"},
			} {
				value := tc.value
				t.Run("record codec "+strings.ReplaceAll(toCycleLabel(value), "/", "_"), func(t *testing.T) {
					q := "WITH RECURSIVE r(n) AS (SELECT $1::text UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path SELECT n,cyc,path FROM r"
					// Logical fields and path lengths are independent of codec escaping.
					logical := strings.Replace(q, "SELECT n,cyc,path FROM r", "SELECT n,cyc,cardinality(path) AS depth FROM r", 1)
					subqueryBoth(subqueryRun(t, p, backend, c), cteBag(logical, []string{"n", "cyc", "depth"}, []string{"TEXT", "BOOL", "INT4"}, [][]any{{value, false, int64(1)}, {value, true, int64(2)}}, value))
					subqueryBoth(subqueryRun(t, p, backend, c), cteBag(q, []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "_RECORD"}, [][]any{{value, false, tc.first}, {value, true, tc.closing}}, value))
				})
			}
		})
	}
}

func toCycleLabel(v any) string {
	if v == nil {
		return "null"
	}
	if v == "" {
		return "empty"
	}
	return v.(string)
}
