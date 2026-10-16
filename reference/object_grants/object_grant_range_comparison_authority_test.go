package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRangeComparisonAuthority(t *testing.T) {
	// These are twelve generic released routines, not one procedure per
	// physical subtype. != is a second spelling of the same <> declaration.
	for _, multi := range []bool{false, true} {
		for _, comparison := range []struct {
			suffix, op           string
			less, equal, greater bool
		}{
			{"eq", "=", false, true, false},
			{"ne", "<>", true, false, true},
			{"lt", "<", true, false, false},
			{"le", "<=", true, true, false},
			{"ge", ">=", false, true, true},
			{"gt", ">", false, false, true},
		} {
			prefix, formal := "range_", "anyrange"
			if multi {
				prefix, formal = "multirange_", "anymultirange"
			}
			name := prefix + comparison.suffix
			t.Run(name, func(t *testing.T) {
				objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
					families := []struct{ typ, a, b string }{
						{"int4range", "[1,4)", "[3,6)"},
						{"int8range", "[1,4)", "[3,6)"},
						{"numrange", "[1,4)", "[3,6)"},
						{"daterange", "[2000-01-01,2000-01-04)", "[2000-01-03,2000-01-06)"},
						{"tsrange", `["2000-01-01 00:00:00","2000-01-04 00:00:00")`, `["2000-01-03 00:00:00","2000-01-06 00:00:00")`},
						{"tstzrange", `["2000-01-01 00:00:00+00","2000-01-04 00:00:00+00")`, `["2000-01-03 00:00:00+00","2000-01-06 00:00:00+00")`},
					}
					if multi {
						families = families[:3] // No temporal-multirange runtime expansion.
						for i := range families {
							families[i].typ = strings.Replace(families[i].typ, "range", "multirange", 1)
							families[i].a = "{" + families[i].a + "}"
							families[i].b = "{" + families[i].b + "}"
						}
					}
					columns := []string{"id INT", "cr int4range", "cs int4range"}
					less := []string{"1", "'[1,4)'", "'[3,6)'"}
					nulls := []string{"2", "NULL", "NULL"}
					empties := []string{"3", "'empty'", "'empty'"}
					greater := []string{"4", "'[3,6)'", "'[1,4)'"}
					equal := []string{"5", "'[1,4)'", "'[1,4)'"}
					for i, f := range families {
						index := strconv.Itoa(i)
						columns = append(columns, "l"+index+" "+f.typ, "r"+index+" "+f.typ)
						less = append(less, "'"+f.a+"'", "'"+f.b+"'")
						nulls = append(nulls, "NULL", "NULL")
						empty := "empty"
						if multi {
							empty = "{}"
						}
						empties = append(empties, "'"+empty+"'", "'"+empty+"'")
						greater = append(greater, "'"+f.b+"'", "'"+f.a+"'")
						equal = append(equal, "'"+f.a+"'", "'"+f.a+"'")
					}
					run(fixture.Exec("CREATE TABLE og_range_comparison_source("+strings.Join(columns, ",")+")", 0))
					rows := []string{"(" + strings.Join(less, ",") + ")", "(" + strings.Join(nulls, ",") + ")", "(" + strings.Join(empties, ",") + ")", "(" + strings.Join(greater, ",") + ")", "(" + strings.Join(equal, ",") + ")"}
					run(fixture.Exec("INSERT INTO og_range_comparison_source VALUES"+strings.Join(rows, ","), 5))
					run(fixture.Exec("GRANT SELECT ON og_range_comparison_source TO og_object_reader", 0))
					var cases []objectGrantProviderCase
					selectRows := func(query string, rows [][]any, args ...any) fixture.Step {
						return fixture.Select(query, []string{"v"}, []string{"BOOL"}, rows, args...)
					}
					selectValue := func(expr, tail string, value any, args ...any) fixture.Step {
						return selectRows("SELECT "+expr+" AS v FROM og_range_comparison_source"+tail, [][]any{{value}}, args...)
					}
					for i, f := range families {
						index := strconv.Itoa(i)
						left, right := "l"+index, "r"+index
						call := left + " " + comparison.op + " " + right
						view := "og_range_comparison_view_" + index
						run(fixture.Exec("CREATE VIEW "+view+" AS SELECT "+call+" AS v FROM og_range_comparison_source WHERE id=1", 0))
						run(fixture.Exec("GRANT SELECT ON "+view+" TO og_object_reader", 0))
						cases = append(cases,
							objectGrantProviderCase{selectValue(call, " WHERE id=1", comparison.less), true, true},
							objectGrantProviderCase{selectValue(left+" OPERATOR(pg_catalog."+comparison.op+") "+right, " WHERE id=1", comparison.less), true, true},
							objectGrantProviderCase{selectRows("SELECT v FROM "+view, [][]any{{comparison.less}}), true, true},
							objectGrantProviderCase{selectValue(call, " WHERE id=2", nil), true, true},
							objectGrantProviderCase{selectValue(call, " WHERE id=3", comparison.equal), true, true},
							objectGrantProviderCase{selectValue(call, " WHERE id=4", comparison.greater), true, true},
							objectGrantProviderCase{selectValue(call, " WHERE id=5", comparison.equal), true, true},
							objectGrantProviderCase{selectRows("SELECT "+call+" AS v FROM og_range_comparison_source WHERE id>99", nil), true, true},
							objectGrantProviderCase{selectRows("SELECT "+call+" AS v FROM og_range_comparison_source WHERE FALSE", nil), true, true},
							objectGrantProviderCase{selectValue("CASE WHEN FALSE THEN "+call+" ELSE TRUE END", " WHERE id=1", true), false, true},
							objectGrantProviderCase{selectValue("(CASE WHEN TRUE THEN NULL ELSE "+left+" END) "+comparison.op+" "+right, " WHERE id=1", nil), false, true},
							objectGrantProviderCase{selectValue(left+" "+comparison.op+" (CASE WHEN TRUE THEN NULL ELSE "+right+" END)", " WHERE id=1", nil), false, true},
						)
						if comparison.op == "<>" {
							cases = append(cases, objectGrantProviderCase{selectValue(left+" != "+right, " WHERE id=1", true), true, true})
						}
						if i == 0 {
							parameterLeft := "(CASE WHEN TRUE THEN $1 ELSE " + left + " END) " + comparison.op + " " + right
							parameterRight := left + " " + comparison.op + " (CASE WHEN TRUE THEN $1 ELSE " + right + " END)"
							constant := "(CASE WHEN TRUE THEN '" + f.a + "' ELSE " + left + " END) " + comparison.op + " (CASE WHEN TRUE THEN '" + f.b + "' ELSE " + right + " END)"
							// Fresh parameters/constants, not a generic-plan or previously
							// constant-folded SAME-held result promise.
							cases = append(cases,
								objectGrantProviderCase{selectValue(parameterLeft, " WHERE id=1", comparison.less, f.a), true, false},
								objectGrantProviderCase{selectValue(parameterRight, " WHERE id=1", comparison.less, f.b), true, false},
								objectGrantProviderCase{selectValue(parameterLeft, " WHERE id=1", nil, nil), false, false},
								objectGrantProviderCase{selectValue(parameterRight, " WHERE id=1", nil, nil), false, false},
								objectGrantProviderCase{selectValue(constant, " WHERE id=1", comparison.less), true, false},
							)
						}
					}
					controlOp, controlValue := "=", false
					if name == "range_eq" {
						controlOp, controlValue = "<>", true
					}
					controls := []fixture.Step{selectValue("cr "+controlOp+" cs", " WHERE id=1", controlValue)}
					objectGrantProviderLifecycle(t, p, b, c, run, "pg_catalog."+name+"("+formal+","+formal+")", "function", name, cases, controls)
				})
			})
		}
	}
}
