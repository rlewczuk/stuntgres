package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Bounded projections amortize protocol round trips without deriving any
// expected value from the native engine. Each batch still has direct and held
// executions, and all nine/27 independent source value combinations.
type comparisonProjection struct {
	expressions, names, types []string
	rows                      [][]any
	from                      string
}

func (v *comparisonProjection) add(t *testing.T, p *fixture.Pair, b fixture.Backend, expression string, values []any) {
	if len(v.rows) == 0 {
		for range values {
			v.rows = append(v.rows, []any{})
		}
	}
	name := fmt.Sprintf("v%d", len(v.names))
	v.expressions = append(v.expressions, expression+" AS "+name)
	v.names = append(v.names, name)
	v.types = append(v.types, "BOOL")
	for i, value := range values {
		v.rows[i] = append(v.rows[i], value)
	}
	if len(v.names) == 32 {
		v.flush(t, p, b)
	}
}

func (v *comparisonProjection) flush(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	if len(v.names) == 0 {
		return
	}
	comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT "+strings.Join(v.expressions, ",")+v.from, v.names, v.types, v.rows))
	v.expressions, v.names, v.types, v.rows = nil, nil, nil, nil
}

// use switches the batch source, flushing expressions bound to the old one.
func (v *comparisonProjection) use(t *testing.T, p *fixture.Pair, b fixture.Backend, from string) {
	if v.from != from {
		v.flush(t, p, b)
		v.from = from
	}
}

func comparisonAnd(a, b any) any {
	if a == false || b == false {
		return false
	}
	if a == nil || b == nil {
		return nil
	}
	return true
}
func comparisonOr(a, b any) any {
	if a == true || b == true {
		return true
	}
	if a == nil || b == nil {
		return nil
	}
	return false
}
func comparisonNot(a any) any {
	if a == nil {
		return nil
	}
	return !a.(bool)
}
func comparisonFiniteResult(left, right, l, r, op int) any {
	if l == 0 || r == 0 {
		return nil
	}
	return comparisonTruth(op, comparisonFiniteSign(left, right, l, r))
}

var comparisonBetweenForms = []string{"BETWEEN", "NOT BETWEEN", "BETWEEN ASYMMETRIC", "NOT BETWEEN ASYMMETRIC", "BETWEEN SYMMETRIC", "NOT BETWEEN SYMMETRIC"}

func comparisonBetween(t *testing.T, family string) {
	for left, typ := range comparisonConcrete {
		if typ.family != family {
			continue
		}
		t.Run(typ.name, func(t *testing.T) {
			// Classify every triple before allocating a fixture. Lowering is
			// (x >= lower AND x <= upper): each bound pair is independent, so
			// execution pairs every eligible lower with itself and with the
			// next eligible identity instead of the full lower x upper product.
			classified := 0
			var eligible []int
			for lower := range comparisonConcrete {
				classified += len(comparisonConcrete)
				if comparisonOrderingPair(left, lower) {
					eligible = append(eligible, lower)
				}
			}
			if classified != 7569 {
				t.Fatalf("classified %d triples, want 7569", classified)
			}
			if len(eligible) == 0 && comparisonOrderingPair(left, left) {
				t.Fatal("orderable left has no admitted triples")
			}
			if len(eligible) == 0 {
				return
			}
			bounds := comparisonBetweenBounds(eligible)
			comparisonFiniteScenario(t, left, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				from := " FROM comparison_values x CROSS JOIN comparison_values l CROSS JOIN comparison_values h"
				order := " ORDER BY x.id,l.id,h.id"
				plainFrom := from + order
				boundFrom := from + " CROSS JOIN " + comparisonFiniteCharSource + order
				projection := comparisonProjection{from: plainFrom}
				for _, bound := range bounds {
					lower, upper := bound[0], bound[1]
					// Lookup-admitted arrays with unavailable leaf ordering are demand tests,
					// not an allowed successful triple or an exclusion from classification.
					xp, lp, hp := lower, left, left
					if comparisonConcrete[upper].name == "pg_node_tree" {
						xp = upper
					}
					boundChar := family == "Strings" && (typ.name == "char" || comparisonConcrete[lower].name == "char" || comparisonConcrete[upper].name == "char")
					charSource := ""
					if boundChar {
						charSource = "comparison_char_source"
					}
					x := comparisonFiniteValueFrom(left, "x", xp, charSource)
					l := comparisonFiniteValueFrom(lower, "l", lp, charSource)
					h := comparisonFiniteValueFrom(upper, "h", hp, charSource)
					if message := comparisonDemand(left, 3); message != "" {
						comparisonExceptionalBetween(t, p, b, x, l, h, message)
						continue
					}
					for form, spelling := range comparisonBetweenForms {
						var values []any
						for a := range 3 {
							for lo := range 3 {
								for hi := range 3 {
									av, lov, hiv := a, lo, hi
									if a != 0 && comparisonConcrete[xp].name == "pg_node_tree" && comparisonStringType(typ.name) {
										av = 1
									}
									if lo != 0 && comparisonConcrete[lp].name == "pg_node_tree" && comparisonStringType(comparisonConcrete[lower].name) {
										lov = 1
									}
									if hi != 0 && comparisonConcrete[hp].name == "pg_node_tree" && comparisonStringType(comparisonConcrete[upper].name) {
										hiv = 1
									}
									ge := comparisonFiniteResult(left, lower, av, lov, 6)
									le := comparisonFiniteResult(left, upper, av, hiv, 4)
									want := comparisonAnd(ge, le)
									if form >= 4 {
										want = comparisonOr(want, comparisonAnd(comparisonFiniteResult(left, upper, av, hiv, 6), comparisonFiniteResult(left, lower, av, lov, 4)))
									}
									if form%2 == 1 {
										want = comparisonNot(want)
									}
									values = append(values, want)
								}
							}
						}
						// add may flush mid-triple. Reset only for a new batch;
						// later ordinary forms must retain any earlier binding.
						if len(projection.names) == 0 {
							projection.from = plainFrom
						}
						if boundChar {
							projection.from = boundFrom
						}
						projection.add(t, p, b, x+" "+spelling+" "+l+" AND "+h, values)
					}
				}
				projection.flush(t, p, b)
			})
		})
	}
}

// Same-type and next-identity bounds: every eligible identity is both a lower
// and an upper bound, alone and mixed. A single eligible identity yields one
// same-type triple.
func comparisonBetweenBounds(eligible []int) [][2]int {
	var bounds [][2]int
	for i, lower := range eligible {
		bounds = append(bounds, [2]int{lower, lower})
		if next := eligible[(i+1)%len(eligible)]; next != lower {
			bounds = append(bounds, [2]int{lower, next})
		}
	}
	return bounds
}

func comparisonOrderingPair(left, right int) bool {
	s := comparisonSignature(left, right)
	return s.operators[2] != 0 && s.operators[3] != 0 && s.operators[4] != 0 && s.operators[5] != 0
}

func TestReferenceComparisonBetweenNumeric(t *testing.T)       { comparisonBetween(t, "Numeric") }
func TestReferenceComparisonBetweenStrings(t *testing.T)       { comparisonBetween(t, "Strings") }
func TestReferenceComparisonBetweenTemporal(t *testing.T)      { comparisonBetween(t, "Temporal") }
func TestReferenceComparisonBetweenIdentifiers(t *testing.T)   { comparisonBetween(t, "Identifiers") }
func TestReferenceComparisonBetweenBinaryNetwork(t *testing.T) { comparisonBetween(t, "BinaryNetwork") }
func TestReferenceComparisonBetweenNegativeSpace(t *testing.T) { comparisonBetween(t, "NegativeSpace") }
func TestReferenceComparisonBetweenRanges(t *testing.T)        { comparisonBetween(t, "Ranges") }
func TestReferenceComparisonBetweenArrays(t *testing.T)        { comparisonBetween(t, "Arrays") }

func TestReferenceComparisonDistinct(t *testing.T) {
	for left, typ := range comparisonConcrete {
		t.Run(typ.name, func(t *testing.T) {
			comparisonFiniteScenario(t, left, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				projection := comparisonProjection{from: " FROM comparison_values l CROSS JOIN comparison_values r ORDER BY l.id,r.id"}
				visited := 0
				for right := range comparisonConcrete {
					visited++
					if comparisonSignature(left, right).operators[0] == 0 {
						continue
					}
					x, y := comparisonFiniteValue(left, "l", right), comparisonFiniteValue(right, "r", left)
					for _, not := range []bool{false, true} {
						form := " IS DISTINCT FROM "
						if not {
							form = " IS NOT DISTINCT FROM "
						}
						if message := comparisonDemand(left, 0); message != "" {
							comparisonDemandRoutes(t, p, b, "SELECT "+x+form+y+" AS v FROM comparison_values l CROSS JOIN comparison_values r WHERE l.id=1 AND r.id=1", message)
							for _, ids := range [][2]int{{0, 0}, {0, 1}, {1, 0}} {
								want := ids[0] != ids[1]
								if not {
									want = !want
								}
								q := fmt.Sprintf("SELECT %s%s%s AS v FROM comparison_values l CROSS JOIN comparison_values r WHERE l.id=%d AND r.id=%d", x, form, y, ids[0], ids[1])
								comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want}}))
							}
							continue
						}
						var values []any
						for l := range 3 {
							for r := range 3 {
								want := l != r
								if l != 0 && r != 0 {
									want = comparisonFiniteSign(left, right, l, r) != 0
								}
								if not {
									want = !want
								}
								values = append(values, want)
							}
						}
						projection.add(t, p, b, x+form+y, values)
					}
				}
				projection.flush(t, p, b)
				if visited != 87 {
					t.Fatalf("visited %d distinct pairs", visited)
				}
			})
		})
	}
}

func TestReferenceComparisonNullTruth(t *testing.T) {
	for left, typ := range comparisonConcrete {
		t.Run(typ.name, func(t *testing.T) {
			comparisonFiniteScenario(t, left, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				value := comparisonFiniteValue(left, "l", -1)
				projection := comparisonProjection{from: " FROM comparison_values l ORDER BY l.id"}
				for i, form := range []string{"IS NULL", "IS NOT NULL", "ISNULL", "NOTNULL"} {
					values := []any{true, false, false}
					if i%2 == 1 {
						values = []any{false, true, true}
					}
					projection.add(t, p, b, value+" "+form, values)
				}
				projection.flush(t, p, b)
				forms := []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"}
				truth := [][]any{{false, true, false, true, true, false}, {true, false, false, true, false, true}, {false, true, true, false, false, true}}
				for i, form := range forms {
					q := "SELECT " + value + " " + form + " AS v FROM comparison_values l ORDER BY l.id"
					if typ.name == "bool" {
						comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{truth[0][i]}, {truth[1][i]}, {truth[2][i]}}))
					} else {
						comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{Code: "42804", Message: "argument of " + form + " must be type boolean, not type " + typ.diagnostic, Position: comparisonCheckpointPosition(q, strings.TrimPrefix(value, "("))})
					}
				}
			})
		})
	}
}
