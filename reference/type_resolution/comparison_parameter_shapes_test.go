package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceComparisonDistinctParameters(t *testing.T) {
	for left, lt := range comparisonConcrete {
		// These three types have catalog/expression routes, not arbitrary textual
		// input. Their DISTINCT occurrences are exercised by the finite source route.
		if lt.element >= 0 || lt.name == "char" || lt.name == "pg_node_tree" || lt.name == "void" {
			continue
		}
		t.Run(lt.name, func(t *testing.T) {
			comparisonFiniteScenario(t, left, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				for right, rt := range comparisonConcrete {
					if rt.element >= 0 || rt.name == "char" || rt.name == "pg_node_tree" || rt.name == "void" || comparisonSignature(left, right).operators[0] == 0 {
						continue
					}
					for _, not := range []bool{false, true} {
						form := "IS DISTINCT FROM"
						if not {
							form = "IS NOT DISTINCT FROM"
						}
						q := "SELECT $1::" + lt.name + " " + form + " $2::" + rt.name + " AS v"
						if lt.name == "bit" || lt.name == "varbit" || rt.name == "bit" || rt.name == "varbit" || strings.Contains(lt.name, "range") || strings.Contains(rt.name, "range") {
							// These scalar codecs are executable through typed destinations,
							// not general parameter CastExpr admission. NULL columns supply
							// the original formal types without altering parameter values.
							x, y := comparisonFiniteValue(left, "l", right), comparisonFiniteValue(right, "r", left)
							q = "SELECT COALESCE($1," + x + ") " + form + " COALESCE($2," + y + ") AS v FROM comparison_values l CROSS JOIN comparison_values r WHERE l.id=0 AND r.id=0"
						}
						stmt, err := b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
						for _, tc := range []struct {
							l, r any
							want bool
						}{
							{lt.low, rt.low, comparisonFiniteSign(left, right, 1, 1) != 0},
							{lt.high, rt.low, comparisonFiniteSign(left, right, 2, 1) != 0},
							{nil, rt.low, true}, {lt.low, nil, true}, {nil, nil, false},
						} {
							want := tc.want
							if not {
								want = !want
							}
							s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, tc.l, tc.r)
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		})
	}
}

func TestReferenceComparisonDomainArrayRoutes(t *testing.T) {
	for _, tc := range []struct{ name, base, low, high string }{
		{"money", "money", "1.00", "2.00"}, {"tid", "tid", "(1,1)", "(1,2)"}, {"range", "int4range", "[1,2)", "[2,3)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN comparison_array_element AS "+tc.base, 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE comparison_array_elements(id integer,v comparison_array_element)", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO comparison_array_elements VALUES(0,NULL),(1,"+comparisonSQLString(tc.low)+"),(2,"+comparisonSQLString(tc.high)+")", 3))
				for op, spelling := range comparisonSpellings {
					var rows [][]any
					for l := range 3 {
						for r := range 3 {
							sign := 0
							if l == 0 && r != 0 {
								sign = 1
							} else if l != 0 && r == 0 {
								sign = -1
							} else if l < r {
								sign = -1
							} else if l > r {
								sign = 1
							}
							rows = append(rows, []any{comparisonTruth(op, sign)})
						}
					}
					q := "SELECT ARRAY[l.v] " + spelling + " ARRAY[r.v] AS v FROM comparison_array_elements l CROSS JOIN comparison_array_elements r ORDER BY l.id,r.id"
					comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"BOOL"}, rows))
				}
				for form, spelling := range comparisonBetweenForms {
					want := form%2 == 0
					q := "SELECT ARRAY[v] " + spelling + " ARRAY[v] AND ARRAY[v] AS v FROM comparison_array_elements ORDER BY id"
					comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want}, {want}, {want}}))
				}
			})
		})
	}
}

func TestReferenceComparisonCycleRecordArray(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		prefix := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path "
		for op, spelling := range comparisonSpellings {
			q := prefix + "SELECT path " + spelling + " path AS v FROM r ORDER BY cardinality(path)"
			want := comparisonTruth(op, 0)
			comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want}, {want}}))
		}
	})
}
