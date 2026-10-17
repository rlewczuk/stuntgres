package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Every lookup-admitted concrete pair, not a handpicked changed-pair shortlist.
// A one-field row deliberately avoids the multi-field B-tree requirement;
// comparison_row/composite contracts independently retain that requirement.
func TestReferenceComparisonConsumers(t *testing.T) {
	for left, typ := range comparisonConcrete {
		t.Run(typ.name, func(t *testing.T) {
			// Visit every spelling before fixture setup, then execute only
			// applicable right windows. At most two right identities share a
			// context, bounding catalog-valued routes without dead fixtures.
			visited := 0
			var rights []int
			for right := range comparisonConcrete {
				applicable := false
				for op := range comparisonSpellings {
					visited++
					if comparisonSignature(left, right).operators[comparisonOperatorSlots[op]] != 0 {
						applicable = true
					}
				}
				if applicable {
					rights = append(rights, right)
				}
			}
			if visited != 609 {
				t.Fatalf("visited %d consumer cells, want 609", visited)
			}
			for start := 0; start < len(rights); start += 2 {
				end := start + 2
				if end > len(rights) {
					end = len(rights)
				}
				t.Run(comparisonConcrete[rights[start]].name, func(t *testing.T) {
					comparisonFiniteScenario(t, left, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						projection := comparisonProjection{from: " FROM comparison_values l CROSS JOIN comparison_values r ORDER BY l.id,r.id"}
						arrayRoutes := map[int]bool{}
						for _, candidate := range comparisonConcrete {
							if candidate.element >= 0 {
								arrayRoutes[candidate.element] = true
							}
						}
						for _, right := range rights[start:end] {
							x, y := comparisonFiniteValue(left, "l", right), comparisonFiniteValue(right, "r", left)
							for op, spelling := range comparisonSpellings {
								if comparisonSignature(left, right).operators[comparisonOperatorSlots[op]] == 0 {
									continue
								}
								var values []any
								for l := range 3 {
									for r := range 3 {
										values = append(values, comparisonFiniteResult(left, right, l, r, op))
									}
								}
								expressions := []string{"ROW(" + x + ") " + spelling + " ROW(" + y + ")"}
								for _, quantifier := range []string{"ANY", "SOME", "ALL"} {
									expressions = append(expressions, x+" "+spelling+" "+quantifier+"(SELECT "+y+")")
									if arrayRoutes[right] {
										expressions = append(expressions, x+" "+spelling+" "+quantifier+"(ARRAY["+y+"]) ")
									}
								}
								if op == 0 || op == 1 {
									keyword := " IN "
									if op == 1 {
										keyword = " NOT IN "
									}
									expressions = append(expressions, x+keyword+"("+y+")", x+keyword+"(SELECT "+y+")")
								}
								for _, expr := range expressions {
									if message := comparisonDemand(left, op); message != "" {
										comparisonDemandRoutes(t, p, b, "SELECT "+expr+" AS v FROM comparison_values l CROSS JOIN comparison_values r WHERE l.id=1 AND r.id=1", message)
									} else {
										projection.add(t, p, b, expr, values)
									}
								}
								for _, quantifier := range []string{"ANY", "SOME", "ALL"} {
									empty := x + " " + spelling + " " + quantifier + "(SELECT " + y + " WHERE FALSE)"
									var emptyValues []any
									for range 9 {
										emptyValues = append(emptyValues, quantifier == "ALL")
									}
									projection.add(t, p, b, empty, emptyValues)
								}
							}
						}
						projection.flush(t, p, b)
					})
				})
			}
		})
	}
}

func TestReferenceComparisonConsumerReductions(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			expression string
			want       any
		}{
			{"NULL::int4 = ANY(ARRAY[]::int4[])", false},
			{"NULL::int4 = SOME(ARRAY[]::int4[])", false},
			{"NULL::int4 = ALL(ARRAY[]::int4[])", true},
			{"1 = ANY(NULL::int4[])", nil}, {"1 = ALL(NULL::int4[])", nil},
			{"1 = ANY(ARRAY[NULL,1,1])", true}, {"1 = ALL(ARRAY[NULL,2,2])", false},
			{"1 = ANY(ARRAY[NULL,2,2])", nil}, {"1 = ALL(ARRAY[NULL,1,1])", nil},
			{"1 IN (NULL,1,1)", true}, {"1 NOT IN (NULL,2,2)", nil},
			{"ROW(0,NULL) < ROW(1,0)", true}, {"ROW(1,NULL) < ROW(1,0)", nil},
		} {
			comparisonCheckpointBool(t, p, b, tc.expression, tc.want)
		}
	})
}
