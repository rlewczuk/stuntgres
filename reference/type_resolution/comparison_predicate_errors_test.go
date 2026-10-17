package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Negative triples collapse to the first failed lowered pair/signature class.
// Enumerate every denied ordered pair at each endpoint, with populated, NULL
// and empty inputs; data cannot suppress an analysis error.
func TestReferenceComparisonPredicateErrors(t *testing.T) {
	for left, typ := range comparisonConcrete {
		t.Run(typ.name, func(t *testing.T) {
			comparisonFiniteScenario(t, left, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				for right := range comparisonConcrete {
					x, y := comparisonFiniteValue(left, "l", right), comparisonFiniteValue(right, "r", left)
					from := " FROM comparison_values l CROSS JOIN comparison_values r"
					if comparisonSignature(left, right).operators[0] == 0 {
						for _, form := range []string{" IS DISTINCT FROM ", " IS NOT DISTINCT FROM "} {
							for _, filter := range []string{"l.id=1 AND r.id=1", "l.id=0 AND r.id=0", "FALSE"} {
								q := "SELECT " + x + form + y + " AS v" + from + " WHERE " + filter
								d := comparisonNoMatch(q, left, right, 0, form)
								d.Position = comparisonCheckpointPosition(q, "IS ")
								comparisonCheckpointAnalysis(t, p, b, q, d)
							}
						}
					}
					if comparisonSignature(left, right).operators[2] != 0 {
						continue
					}
					for form, spelling := range comparisonBetweenForms {
						op := 6
						if form%2 == 1 {
							op = 3
						}
						for endpoint := range 2 {
							lo, hi := y, x
							if endpoint == 1 {
								lo, hi = x, y
							}
							// If x itself has no ordering, that earlier x/x failure wins when
							// the incompatible endpoint is upper; released lowering is ordered.
							deniedRight := right
							firstFails := endpoint == 1 && comparisonSignature(left, left).operators[comparisonOperatorSlots[op]] == 0
							if firstFails {
								deniedRight = left
							}
							for _, filter := range []string{"l.id=1 AND r.id=1", "l.id=0 AND r.id=0", "FALSE"} {
								q := "SELECT " + x + " " + spelling + " " + lo + " AND " + hi + " AS v" + from + " WHERE " + filter
								actualOp := op
								if endpoint == 1 && !firstFails {
									if form%2 == 0 {
										actualOp = 4
									} else {
										actualOp = 5
									}
								}
								d := comparisonNoMatch(q, left, deniedRight, actualOp, spelling)
								// Released grammar gives A_Expr the NOT location for
								// NOT BETWEEN; every lowered operator inherits it.
								d.Position = comparisonCheckpointPosition(q, spelling)
								comparisonCheckpointAnalysis(t, p, b, q, d)
							}
						}
					}
				}
			})
		})
	}
}

func TestReferenceComparisonNullOnlyRoutes(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE comparison_null_route(v integer)", 0))
		for _, value := range []string{"NULL::record", "(SELECT attmissingval FROM pg_attribute WHERE attrelid='comparison_null_route'::regclass AND attnum=1)", "NULL::information_schema.time_stamp"} {
			for i, form := range []string{"IS NULL", "IS NOT NULL", "ISNULL", "NOTNULL"} {
				comparisonCheckpointBool(t, p, b, value+" "+form, i%2 == 0)
			}
		}
	})
}
