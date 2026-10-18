package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Negative triples collapse to the first failed lowered pair/signature class.
// Enumerate every denied ordered pair at each endpoint, with populated, NULL
// and empty inputs; data cannot suppress an analysis error.
func TestReferenceComparisonPredicateErrorsNumeric(t *testing.T) {
	comparisonPredicateErrors(t, "Numeric")
}

func TestReferenceComparisonPredicateErrorsStrings(t *testing.T) {
	comparisonPredicateErrors(t, "Strings")
}

func TestReferenceComparisonPredicateErrorsTemporal(t *testing.T) {
	comparisonPredicateErrors(t, "Temporal")
}

func TestReferenceComparisonPredicateErrorsIdentifiers(t *testing.T) {
	comparisonPredicateErrors(t, "Identifiers")
}

func TestReferenceComparisonPredicateErrorsBinaryNetwork(t *testing.T) {
	comparisonPredicateErrors(t, "BinaryNetwork")
}

func TestReferenceComparisonPredicateErrorsNegativeSpace(t *testing.T) {
	comparisonPredicateErrors(t, "NegativeSpace")
}

func TestReferenceComparisonPredicateErrorsRanges(t *testing.T) {
	comparisonPredicateErrors(t, "Ranges")
}

func TestReferenceComparisonPredicateErrorsArrays(t *testing.T) {
	comparisonPredicateErrors(t, "Arrays")
}

const comparisonPredicateErrorsRightWindow = 8

func comparisonPredicateErrors(t *testing.T, family string) {
	t.Helper()
	for left, typ := range comparisonConcrete {
		if typ.family != family {
			continue
		}
		t.Run(typ.name, func(t *testing.T) {
			for start := 0; start < len(comparisonConcrete); start += comparisonPredicateErrorsRightWindow {
				end := min(start+comparisonPredicateErrorsRightWindow, len(comparisonConcrete))
				t.Run(fmt.Sprintf("right_%02d_%02d", start, end), func(t *testing.T) {
					comparisonPredicateErrorsWindow(t, left, start, end)
				})
			}
		})
	}
}

// Each right window owns its fresh scenario, including setup and cleanup.
func comparisonPredicateErrorsWindow(t *testing.T, left, start, end int) {
	t.Helper()
	comparisonFiniteScenario(t, left, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for right := start; right < end; right++ {
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
