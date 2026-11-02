package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangeCorrelation(t *testing.T) {
	overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE TABLE range_corr_src(id integer,v text)", "LOAD 'pg_overexplain'"} {
			overexplainExec(t, p, b, c, q)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO range_corr_src VALUES(1,'one'),(2,NULL),(3,'three')", 3))
		cases := []struct {
			name, child                                     string
			nested, correlated, middleCorrelated, twoOwners bool
		}{
			{"immediate", "SELECT a.id,(SELECT b.id FROM range_corr_src b WHERE b.id=a.id AND b.tableoid=a.tableoid OFFSET 0) AS captured FROM range_corr_src a ORDER BY a.id", false, true, true, false},
			{"ancestor-only", "SELECT a.id,(SELECT (SELECT c.id FROM range_corr_src c WHERE c.id=a.id AND c.tableoid=a.tableoid OFFSET 0) FROM range_corr_src b WHERE b.id=1 OFFSET 0) AS captured FROM range_corr_src a ORDER BY a.id", true, true, false, false},
			{"uncorrelated", "SELECT a.id,(SELECT (SELECT c.id FROM range_corr_src c WHERE c.id=1 OFFSET 0) FROM range_corr_src b WHERE b.id=1 OFFSET 0) AS captured FROM range_corr_src a ORDER BY a.id", true, false, false, false},
			{"two-direct-owners", "SELECT a.id,(SELECT (SELECT c.id FROM range_corr_src c WHERE c.id=a.id AND c.id=b.id OFFSET 0) FROM range_corr_src b WHERE b.id=a.id OFFSET 0) AS captured FROM range_corr_src a ORDER BY a.id", true, true, true, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				q := overexplainSQL("JSON", "RANGE_TABLE, VERBOSE, COSTS FALSE", tc.child)
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				check := func(r overexplainResult) {
					entries := overexplainRange(t, b, r, "JSON", true)
					aliases := []string{"a", "b"}
					if tc.nested {
						aliases = append(aliases, "c")
					}
					owners := map[string]map[string]any{}
					for _, alias := range aliases {
						entry := overexplainRangeNamed(t, b, entries, "relation", alias)
						if b.PG {
							if entry["Relation"] != "public.range_corr_src" || overexplainInteger(t, entry["Permission Info Index"], false) <= 0 {
								t.Fatal("PG retained scalar relation/permission identity", entry)
							}
						} else {
							overexplainRangeRelation(t, p, c, entry, "range_corr_src", "JSON")
							owners[alias] = overexplainRangeOwnerScope(t, r, entry["Entry ID"])
						}
					}
					if b.PG {
						found := false
						for _, node := range explainJSONNodes(r.document) {
							if name, ok := node["Subplan Name"].(string); ok && name != "" {
								found = true
							}
						}
						if !found {
							t.Fatal("PG fixture lost actual scalar subplan ownership", r.document)
						}
						return
					}
					root, middle := owners["a"], owners["b"]
					if root["Scope ID"] != float64(1) || middle["Kind"] != "subplan" || middle["Parent Scope ID"] != root["Scope ID"] {
						t.Fatal("incorrect immediate lexical ownership", owners)
					}
					wantMiddle := []any{}
					if tc.middleCorrelated {
						wantMiddle = append(wantMiddle, root["Scope ID"])
					}
					overexplainRangeOuterScopes(t, middle, wantMiddle)
					if tc.nested {
						inner := owners["c"]
						if inner["Kind"] != "subplan" || inner["Parent Scope ID"] != middle["Scope ID"] {
							t.Fatal("incorrect nested lexical parent", owners)
						}
						wantInner := []any{}
						if tc.correlated {
							wantInner = append(wantInner, root["Scope ID"])
						}
						if tc.twoOwners {
							wantInner = append(wantInner, middle["Scope ID"])
						}
						overexplainRangeOuterScopes(t, inner, wantInner)
					}
				}
				for _, prepared := range []bool{false, true} {
					check(overexplainQuery(t, p, c, q, "JSON", prepared))
				}
				for range 2 {
					rows, err := held.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					check(overexplainRows(t, rows, "JSON"))
				}
				want := [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}
				if !tc.correlated {
					want = [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(1)}}
				}
				values := fixture.Select(tc.child, []string{"id", "captured"}, []string{"INT4", "INT4"}, want)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, values)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(values))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM range_corr_src ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
	})
}

func overexplainRangeOuterScopes(t *testing.T, scope map[string]any, wants []any) {
	t.Helper()
	value, exists := scope["Outer Scope IDs"]
	if len(wants) == 0 {
		if exists {
			t.Fatal("uncorrelated query invented captured outer owners", scope)
		}
		return
	}
	if !exists {
		t.Fatal("correlated query omitted actual captured outer owners", scope, wants)
	}
	got, want := map[any]int{}, map[any]int{}
	previous := int64(0)
	for _, id := range overexplainRangeList(t, value) {
		current := overexplainInteger(t, id, false)
		if current <= previous {
			t.Fatal("captured outer owners are not unique ascending scope IDs", scope)
		}
		previous = current
		got[id]++
	}
	for _, id := range wants {
		want[id]++
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("direct captured outer-owner set/multiplicity differs", got, want, scope)
	}
}
