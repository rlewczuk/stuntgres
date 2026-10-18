package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Negative triples collapse to the first failed lowered pair/signature class.
// Every denied ordered pair is witnessed at each endpoint; populated, NULL and
// empty inputs rotate because data cannot suppress an analysis error.
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

// One serialized scenario per left identity visits every right in order. An
// empty pair keeps one DISTINCT witness and one BETWEEN witness at each
// endpoint, on rotating forms, filters and routes; any other denied pair
// keeps every form, filter and route.
func comparisonPredicateErrors(t *testing.T, family string) {
	t.Helper()
	comparisonAssertPredicateRotation(t, family)
	for left, typ := range comparisonConcrete {
		if typ.family != family {
			continue
		}
		t.Run(typ.name, func(t *testing.T) {
			comparisonFiniteScenario(t, left, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				for right := range comparisonConcrete {
					comparisonPredicateErrorsPair(t, p, b, left, right)
				}
			})
		})
	}
}

type comparisonPredicateWitness struct {
	form, filter int
	prepared     bool
}

// Full enumeration of forms x filters x routes for non-empty denied pairs.
func comparisonPredicateWitnesses(forms int) []comparisonPredicateWitness {
	var all []comparisonPredicateWitness
	for form := range forms {
		for filter := range comparisonAnalysisFilters {
			for _, prepared := range []bool{false, true} {
				all = append(all, comparisonPredicateWitness{form, filter, prepared})
			}
		}
	}
	return all
}

func comparisonPredicateErrorsPair(t *testing.T, p *fixture.Pair, b fixture.Backend, left, right int) {
	t.Helper()
	x, y := comparisonFiniteValue(left, "l", right), comparisonFiniteValue(right, "r", left)
	from := " FROM comparison_values l CROSS JOIN comparison_values r"
	empty := comparisonEmptyPair(left, right)
	if comparisonSignature(left, right).operators[0] == 0 {
		witnesses := comparisonPredicateWitnesses(len(comparisonDistinctForms))
		if empty {
			form, filter, prepared := comparisonDistinctWitness(left, right)
			witnesses = []comparisonPredicateWitness{{form, filter, prepared}}
		}
		for _, w := range witnesses {
			q := "SELECT " + x + comparisonDistinctForms[w.form] + y + " AS v" + from + " WHERE " + comparisonAnalysisFilters[w.filter]
			d := comparisonNoMatch(q, left, right, 0, comparisonDistinctForms[w.form])
			d.Position = comparisonCheckpointPosition(q, "IS ")
			comparisonCheckpointAnalysisRoute(t, p, b, q, d, w.prepared)
		}
	}
	if comparisonSignature(left, right).operators[2] != 0 {
		return
	}
	for endpoint := range 2 {
		witnesses := comparisonPredicateWitnesses(len(comparisonBetweenForms))
		if empty {
			form, filter, prepared := comparisonBetweenWitness(left, right, endpoint)
			witnesses = []comparisonPredicateWitness{{form, filter, prepared}}
		}
		for _, w := range witnesses {
			comparisonBetweenDenial(t, p, b, left, right, endpoint, w, x, y, from)
		}
	}
}

func comparisonBetweenDenial(t *testing.T, p *fixture.Pair, b fixture.Backend, left, right, endpoint int, w comparisonPredicateWitness, x, y, from string) {
	t.Helper()
	spelling := comparisonBetweenForms[w.form]
	op := 6
	if w.form%2 == 1 {
		op = 3
	}
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
	q := "SELECT " + x + " " + spelling + " " + lo + " AND " + hi + " AS v" + from + " WHERE " + comparisonAnalysisFilters[w.filter]
	actualOp := op
	if endpoint == 1 && !firstFails {
		if w.form%2 == 0 {
			actualOp = 4
		} else {
			actualOp = 5
		}
	}
	d := comparisonNoMatch(q, left, deniedRight, actualOp, spelling)
	// Released grammar gives A_Expr the NOT location for
	// NOT BETWEEN; every lowered operator inherits it.
	d.Position = comparisonCheckpointPosition(q, spelling)
	comparisonCheckpointAnalysisRoute(t, p, b, q, d, w.prepared)
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
