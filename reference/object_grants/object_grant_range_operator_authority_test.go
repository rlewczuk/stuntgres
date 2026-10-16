package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The 42 identities and full generic formals are independently pinned in
// PostgreSQL 18.0. Physical range subtypes select instances, not leaf EXECUTE
// checks or extra concrete overloads. Value expectations use overlapping
// [1,4) and [3,6) spans (and the corresponding finite temporal spans).
func TestReferenceObjectGrantRangeOperatorAuthority(t *testing.T) {
	type variant struct{ name, op, left, right string }
	variants := []variant{
		{"range_contains", "@>", "r", "r"},
		{"range_contains_elem", "@>", "r", "e"},
		{"range_contains_multirange", "@>", "r", "m"},
		{"multirange_contains_range", "@>", "m", "r"},
		{"multirange_contains_multirange", "@>", "m", "m"},
		{"multirange_contains_elem", "@>", "m", "e"},
		{"range_contained_by", "<@", "r", "r"},
		{"elem_contained_by_range", "<@", "e", "r"},
		{"range_contained_by_multirange", "<@", "r", "m"},
		{"multirange_contained_by_range", "<@", "m", "r"},
		{"multirange_contained_by_multirange", "<@", "m", "m"},
		{"elem_contained_by_multirange", "<@", "e", "m"},
		{"range_overlaps", "&&", "r", "r"},
		{"range_overlaps_multirange", "&&", "r", "m"},
		{"multirange_overlaps_range", "&&", "m", "r"},
		{"multirange_overlaps_multirange", "&&", "m", "m"},
		{"range_before", "<<", "r", "r"},
		{"range_before_multirange", "<<", "r", "m"},
		{"multirange_before_range", "<<", "m", "r"},
		{"multirange_before_multirange", "<<", "m", "m"},
		{"range_after", ">>", "r", "r"},
		{"range_after_multirange", ">>", "r", "m"},
		{"multirange_after_range", ">>", "m", "r"},
		{"multirange_after_multirange", ">>", "m", "m"},
		{"range_overleft", "&<", "r", "r"},
		{"range_overleft_multirange", "&<", "r", "m"},
		{"multirange_overleft_range", "&<", "m", "r"},
		{"multirange_overleft_multirange", "&<", "m", "m"},
		{"range_overright", "&>", "r", "r"},
		{"range_overright_multirange", "&>", "r", "m"},
		{"multirange_overright_range", "&>", "m", "r"},
		{"multirange_overright_multirange", "&>", "m", "m"},
		{"range_adjacent", "-|-", "r", "r"},
		{"range_adjacent_multirange", "-|-", "r", "m"},
		{"multirange_adjacent_range", "-|-", "m", "r"},
		{"multirange_adjacent_multirange", "-|-", "m", "m"},
		{"range_union", "+", "r", "r"},
		{"multirange_union", "+", "m", "m"},
		{"range_intersect", "*", "r", "r"},
		{"multirange_intersect", "*", "m", "m"},
		{"range_minus", "-", "r", "r"},
		{"multirange_minus", "-", "m", "m"},
	}
	type family struct {
		rangeType, multiType, elementType              string
		a, b, element, union, intersection, difference string
	}
	families := []family{
		{"int4range", "int4multirange", "integer", "[1,4)", "[3,6)", "3", "[1,6)", "[3,4)", "[1,3)"},
		{"int8range", "int8multirange", "bigint", "[1,4)", "[3,6)", "3", "[1,6)", "[3,4)", "[1,3)"},
		{"numrange", "nummultirange", "numeric", "[1,4)", "[3,6)", "3", "[1,6)", "[3,4)", "[1,3)"},
		{"daterange", "", "date", "[2000-01-01,2000-01-04)", "[2000-01-03,2000-01-06)", "2000-01-03", "[2000-01-01,2000-01-06)", "[2000-01-03,2000-01-04)", "[2000-01-01,2000-01-03)"},
		{"tsrange", "", "timestamp", `["2000-01-01 00:00:00","2000-01-04 00:00:00")`, `["2000-01-03 00:00:00","2000-01-06 00:00:00")`, "2000-01-03 00:00:00", `["2000-01-01 00:00:00","2000-01-06 00:00:00")`, `["2000-01-03 00:00:00","2000-01-04 00:00:00")`, `["2000-01-01 00:00:00","2000-01-03 00:00:00")`},
		{"tstzrange", "", "timestamptz", `["2000-01-01 00:00:00+00","2000-01-04 00:00:00+00")`, `["2000-01-03 00:00:00+00","2000-01-06 00:00:00+00")`, "2000-01-03 00:00:00+00", `["2000-01-01 00:00:00+00","2000-01-06 00:00:00+00")`, `["2000-01-03 00:00:00+00","2000-01-04 00:00:00+00")`, `["2000-01-01 00:00:00+00","2000-01-03 00:00:00+00")`},
	}
	formal := map[string]string{"r": "anyrange", "m": "anymultirange", "e": "anyelement"}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				physical := families
				if v.left == "m" || v.right == "m" {
					physical = physical[:3] // Existing native multirange families only.
				}
				columns := []string{"id INT", "cr int4range", "cs int4range"}
				values := []string{"1", "'[1,4)'", "'[3,6)'"}
				nulls := []string{"2", "NULL", "NULL"}
				empties := []string{"3", "'empty'", "'empty'"}
				operand := func(f family, kind string, left bool) (typ, input, empty string) {
					if kind == "e" {
						return f.elementType, f.element, f.element
					}
					input = f.b
					if left {
						input = f.a
					}
					if kind == "m" {
						return f.multiType, "{" + input + "}", "{}"
					}
					return f.rangeType, input, "empty"
				}
				for i, f := range physical {
					for j, kind := range []string{v.left, v.right} {
						typ, input, empty := operand(f, kind, j == 0)
						column := "l" + strconv.Itoa(i)
						if j == 1 {
							column = "r" + strconv.Itoa(i)
						}
						columns = append(columns, column+" "+typ)
						values = append(values, "'"+input+"'")
						nulls = append(nulls, "NULL")
						empties = append(empties, "'"+empty+"'")
					}
				}
				run(fixture.Exec("CREATE TABLE og_range_operator_source("+strings.Join(columns, ",")+")", 0))
				run(fixture.Exec("INSERT INTO og_range_operator_source VALUES("+strings.Join(values, ",")+"),("+strings.Join(nulls, ",")+"),("+strings.Join(empties, ",")+")", 3))
				run(fixture.Exec("GRANT SELECT ON og_range_operator_source TO og_object_reader", 0))
				var cases []objectGrantProviderCase
				for i, f := range physical {
					left, right := "l"+strconv.Itoa(i), "r"+strconv.Itoa(i)
					call := left + " " + v.op + " " + right
					typ := "BOOL"
					var want any
					var empty any = false
					switch v.op {
					case "+", "*", "-":
						typ, want, empty = strings.ToUpper(f.rangeType), f.union, "empty"
						if v.op == "*" {
							want = f.intersection
						} else if v.op == "-" {
							want = f.difference
						}
						if v.left == "m" {
							typ, want, empty = strings.ToUpper(f.multiType), "{"+want.(string)+"}", "{}"
						}
					case "&&", "&<":
						want = true
					default:
						want = false
						if v.op == "@>" || v.op == "<@" {
							if v.left == "e" || v.right == "e" {
								want = true
							} else {
								empty = true
							}
						}
					}
					selectRows := func(query string, rows [][]any, args ...any) fixture.Step {
						resultType := typ
						if b.PG && strings.HasSuffix(typ, "MULTIRANGE") {
							resultType = "" // Pinned pq has no multirange OID names.
						}
						s := fixture.Select(query, []string{"v"}, []string{resultType}, rows, args...)
						if typ != "BOOL" {
							s.CheckRaw = func(t *testing.T, _, _ int, value any) bool {
								if value == nil {
									return true
								}
								if b.PG {
									raw, ok := value.([]byte)
									if !ok || raw == nil {
										t.Fatalf("range result raw carrier=%T, want nonnil []byte", value)
									}
								} else if _, ok := value.(string); !ok {
									t.Fatalf("range result raw carrier=%T, want string", value)
								}
								return true
							}
						}
						return s
					}
					selectValue := func(expr, tail string, value any, args ...any) fixture.Step {
						return selectRows("SELECT "+expr+" AS v FROM og_range_operator_source"+tail, [][]any{{value}}, args...)
					}
					view := "og_range_operator_view_" + strconv.Itoa(i)
					run(fixture.Exec("CREATE VIEW "+view+" AS SELECT "+call+" AS v FROM og_range_operator_source WHERE id=1", 0))
					run(fixture.Exec("GRANT SELECT ON "+view+" TO og_object_reader", 0))
					cases = append(cases,
						objectGrantProviderCase{selectValue(call, " WHERE id=1", want), true, true},
						objectGrantProviderCase{selectValue(left+" OPERATOR(pg_catalog."+v.op+") "+right, " WHERE id=1", want), true, true},
						objectGrantProviderCase{selectRows("SELECT v FROM "+view, [][]any{{want}}), true, true},
						objectGrantProviderCase{selectValue(call, " WHERE id=2", nil), true, true},
						objectGrantProviderCase{selectValue(call, " WHERE id=3", empty), true, true},
						objectGrantProviderCase{selectRows("SELECT "+call+" AS v FROM og_range_operator_source WHERE id>99", nil), true, true},
						objectGrantProviderCase{selectRows("SELECT "+call+" AS v FROM og_range_operator_source WHERE FALSE", nil), true, true},
						objectGrantProviderCase{selectValue("CASE WHEN FALSE THEN "+call+" ELSE NULL END", " WHERE id=1", nil), false, true},
						objectGrantProviderCase{selectValue("(CASE WHEN TRUE THEN NULL ELSE "+left+" END) "+v.op+" "+right, " WHERE id=1", nil), false, true},
						objectGrantProviderCase{selectValue(left+" "+v.op+" (CASE WHEN TRUE THEN NULL ELSE "+right+" END)", " WHERE id=1", nil), false, true},
					)
					if i == 0 {
						_, leftInput, _ := operand(f, v.left, true)
						_, rightInput, _ := operand(f, v.right, false)
						parameterLeft := "(CASE WHEN TRUE THEN $1 ELSE " + left + " END) " + v.op + " " + right
						parameterRight := left + " " + v.op + " (CASE WHEN TRUE THEN $1 ELSE " + right + " END)"
						// Fresh/custom parameter calls, not a generic-plan NULL promise.
						// Fully constant immutable calls likewise must be fresh after
						// each revoke: a previously held PG result can be folded.
						constant := "(CASE WHEN TRUE THEN '" + leftInput + "' ELSE " + left + " END) " + v.op + " (CASE WHEN TRUE THEN '" + rightInput + "' ELSE " + right + " END)"
						// PG18 support replaces a constant single range containing a
						// variable element with subtype bound comparisons. Bind only
						// the element side for those two signatures, so these remain
						// actual range routine invocations on both backends.
						if v.name != "range_contains_elem" {
							cases = append(cases, objectGrantProviderCase{selectValue(parameterLeft, " WHERE id=1", want, leftInput), true, false})
						}
						if v.name != "elem_contained_by_range" {
							cases = append(cases, objectGrantProviderCase{selectValue(parameterRight, " WHERE id=1", want, rightInput), true, false})
						}
						cases = append(cases,
							objectGrantProviderCase{selectValue(parameterLeft, " WHERE id=1", nil, nil), false, false},
							objectGrantProviderCase{selectValue(parameterRight, " WHERE id=1", nil, nil), false, false},
							objectGrantProviderCase{selectValue(constant, " WHERE id=1", want), true, false},
						)
					}
				}
				controlOp, controlValue := "&&", true
				if v.name == "range_overlaps" {
					controlOp, controlValue = "@>", false
				}
				controls := []fixture.Step{fixture.Select("SELECT cr "+controlOp+" cs AS v FROM og_range_operator_source WHERE id=1", []string{"v"}, []string{"BOOL"}, [][]any{{controlValue}})}
				target := "pg_catalog." + v.name + "(" + formal[v.left] + "," + formal[v.right] + ")"
				objectGrantProviderLifecycle(t, p, b, c, run, target, "function", v.name, cases, controls)
			})
		})
	}
}
