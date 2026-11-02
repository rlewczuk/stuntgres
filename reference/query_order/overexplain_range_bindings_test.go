package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangeBindings(t *testing.T) {
	overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		overexplainRangeSeed(t, p, b, c)
		overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
		capture := func(child string, prepared bool) (overexplainResult, []map[string]any) {
			r := overexplainQuery(t, p, c, overexplainSQL("JSON", "RANGE_TABLE, VERBOSE, COSTS FALSE", child), "JSON", prepared)
			return r, overexplainRange(t, b, r, "JSON", true)
		}
		for _, prepared := range []bool{false, true} {
			_, entries := capture("SELECT a.id,b.v FROM public.range_src a JOIN public.range_src b ON a.id=b.id", prepared)
			a := overexplainRangeNamed(t, b, entries, "relation", "a")
			bb := overexplainRangeNamed(t, b, entries, "relation", "b")
			if b.PG {
				for _, e := range []map[string]any{a, bb} {
					if e["Relation"] != "public.range_src" || e["Relation Kind"] != "relation" || e["Relation Lock Mode"] != "AccessShareLock" || overexplainInteger(t, e["Permission Info Index"], false) <= 0 {
						t.Fatal("PG relation/permission reference", e)
					}
				}
				if a["Eref"] != "a (id, v)" || bb["Eref"] != "b (id, v)" || a["Alias"] != "a ()" || bb["Alias"] != "b ()" || a["RTI"] == bb["RTI"] {
					t.Fatal("PG real self-join aliases/RTIs", a, bb)
				}
			} else {
				for _, e := range []map[string]any{a, bb} {
					overexplainRangeRelation(t, p, c, e, "public.range_src", "JSON")
					if !reflect.DeepEqual(overexplainRangeStrings(t, e["Columns"]), []string{"id", "v"}) || e["Role"] != "source" {
						t.Fatal("actual exposed source columns", e)
					}
				}
				if a["Relation OID"] != bb["Relation OID"] || a["Entry ID"] == bb["Entry ID"] || a["Occurrence"] == bb["Occurrence"] {
					t.Fatal("self-join collapsed occurrences", a, bb)
				}
			}
			_, entries = capture("SELECT j.a_id,j.b_v FROM (range_src a JOIN range_src b ON a.id=b.id) AS j(a_id,a_v,b_id,b_v)", prepared)
			j := overexplainRangeNamed(t, b, entries, "join", "j")
			if j["Join Type"] != "Inner" {
				t.Fatal("actual join kind", j)
			}
			if b.PG {
				if j["Alias"] != "j (a_id, a_v, b_id, b_v)" || j["Eref"] != "j (a_id, a_v, b_id, b_v)" || overexplainInteger(t, j["JOIN USING Columns"], false) != 0 {
					t.Fatal("PG accepted joined namespace", j)
				}
			} else {
				if !reflect.DeepEqual(overexplainRangeStrings(t, j["Columns"]), []string{"a_id", "a_v", "b_id", "b_v"}) {
					t.Fatal("lost join alias labels", j)
				}
				left := overexplainRangeNamed(t, b, entries, "relation", "a")
				right := overexplainRangeNamed(t, b, entries, "relation", "b")
				if j["Left Entry ID"] != left["Entry ID"] || j["Right Entry ID"] != right["Entry ID"] {
					t.Fatal("join owner/input links", j, left, right)
				}
			}
			_, entries = capture(`SELECT "Odd Alias"."001", "Odd Alias"."v & <" FROM public.range_src AS "Odd Alias"("001", "v & <")`, prepared)
			quoted := overexplainRangeNamed(t, b, entries, "relation", "Odd Alias")
			if b.PG {
				if quoted["Eref"] != `"Odd Alias" ("001", "v & <")` {
					t.Fatal("PG identifier escaping", quoted)
				}
			} else if !reflect.DeepEqual(overexplainRangeStrings(t, quoted["Columns"]), []string{"001", "v & <"}) {
				t.Fatal("native identifier values", quoted)
			}
		}
		t.Run("nested-shadow-and-correlated", func(t *testing.T) {
			child := "SELECT a.id,outer_d.v,(SELECT a.v FROM range_src a WHERE a.id=outer_d.id OFFSET 0) AS scalar_v FROM range_src a JOIN (SELECT a.id,a.v FROM range_src a OFFSET 0) outer_d ON outer_d.id=a.id"
			r, entries := capture(child, true)
			d := overexplainRangeNamed(t, b, entries, "subquery", "outer_d")
			if b.PG {
				if d["Eref"] != "outer_d (id, v)" {
					t.Fatal("PG nonflattened derived range", d)
				}
				count := 0
				for _, e := range entries {
					if e["Kind"] == "relation" && e["Eref"] == "a (id, v)" {
						count++
					}
				}
				if count != 3 {
					t.Fatal("PG real shadowed relation occurrences", entries)
				}
			} else {
				if _, exists := d["Child Scope ID"]; !exists {
					t.Fatal("derived owner lacks scope", d)
				}
				scopes := overexplainRangeList(t, overexplainRangeMap(t, r.document[0]["Embedded Range Table"])["Scopes"])
				shadowScopes := map[int64]bool{}
				hasSubplan := false
				for _, item := range scopes {
					s := overexplainRangeMap(t, item)
					if s["Kind"] == "subplan" {
						hasSubplan = true
						if overexplainInteger(t, s["Parent Scope ID"], false) != 1 {
							t.Fatal("correlated scalar lost its actual outer query owner", s)
						}
					}
					if s["Scope ID"] == d["Child Scope ID"] && (overexplainInteger(t, s["Parent Scope ID"], false) != 1 || s["Owner Entry ID"] != d["Entry ID"]) {
						t.Fatal("derived scope is not owned by its actual outer range", s, d)
					}
					for _, item := range overexplainRangeList(t, s["Entries"]) {
						e := overexplainRangeMap(t, item)
						if e["Kind"] == "relation" && e["Range Name"] == "a" {
							shadowScopes[overexplainInteger(t, s["Scope ID"], false)] = true
						}
					}
				}
				if len(shadowScopes) != 3 || !hasSubplan {
					t.Fatal("alias shadowing or scalar ownership collapsed", scopes)
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(child+" ORDER BY a.id", []string{"id", "v", "scalar_v"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "one", "one"}, {int64(2), nil, nil}, {int64(3), "three", "three"}}))
		})
		t.Run("shared-cte", func(t *testing.T) {
			_, entries := capture("WITH x AS MATERIALIZED (SELECT id,v FROM range_src) SELECT l.id,r.v FROM x l JOIN x r ON l.id=r.id", true)
			l := overexplainRangeNamed(t, b, entries, "cte", "l")
			r := overexplainRangeNamed(t, b, entries, "cte", "r")
			if b.PG {
				if l["CTE Name"] != "x" || r["CTE Name"] != "x" || l["CTE Self-Reference"] != false || r["CTE Self-Reference"] != false {
					t.Fatal("PG CTE metadata", l, r)
				}
			} else if l["Entry ID"] == r["Entry ID"] || l["Producer Scope ID"] == nil || l["Producer Scope ID"] != r["Producer Scope ID"] {
				t.Fatal("CTE reader/producer ownership", l, r)
			}
		})
		t.Run("recursive-worktable", func(t *testing.T) {
			_, entries := capture("WITH RECURSIVE walk(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM walk WHERE n<3) SELECT n FROM walk", false)
			found := false
			for _, e := range entries {
				if b.PG && e["Kind"] == "cte" && e["CTE Self-Reference"] == true {
					found = true
				}
				if !b.PG && e["Kind"] == "worktable" && e["Producer Scope ID"] != nil {
					found = true
				}
			}
			if !found {
				t.Fatal("missing terminating recursive reader edge", entries)
			}
		})
		for _, tc := range []struct {
			child, kind, name string
			columns           []string
		}{
			{"SELECT vals.n,vals.v FROM (VALUES(1,'one'::text),(2,NULL::text)) vals(n,v)", "values", "vals", []string{"n", "v"}},
			{"SELECT fn.n FROM generate_series(1,3) fn(n)", "function", "fn", []string{"n"}},
		} {
			t.Run(tc.kind, func(t *testing.T) {
				_, entries := capture(tc.child, true)
				kind := tc.kind
				if b.PG && tc.kind == "values" {
					// PostgreSQL retains the accepted alias on an outer
					// subquery RTE, independently of its inner VALUES RTE.
					kind = "subquery"
					values := overexplainRangeNamed(t, b, entries, "values", "*VALUES*")
					if len(entries) != 2 || values["Eref"] != `"*VALUES*" (column1, column2)` {
						t.Fatal("PG inner VALUES range", entries)
					}
					if _, exists := values["Relation"]; exists {
						t.Fatal("PG VALUES invented a physical relation", values)
					}
				}
				e := overexplainRangeNamed(t, b, entries, kind, tc.name)
				if b.PG && tc.kind == "values" && (e["Alias"] != "vals (n, v)" || e["Eref"] != "vals (n, v)") {
					t.Fatal("PG accepted VALUES subquery alias", e)
				}
				if !b.PG && !reflect.DeepEqual(overexplainRangeStrings(t, e["Columns"]), tc.columns) {
					t.Fatal("range effective labels", e)
				}
				if b.PG && tc.kind == "function" && e["WITH ORDINALITY"] != false {
					t.Fatal("PG function ordinality", e)
				}
			})
		}
		overexplainRangeValues(t, p, b, c)
	})
}
