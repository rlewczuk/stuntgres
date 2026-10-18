package reference_test

import (
	"fmt"
	"testing"
)

// Released analysis fails before any row is read, so these filters cannot
// suppress a no-match diagnostic: populated, all-NULL and empty inputs.
var comparisonAnalysisFilters = []string{"l.id=1 AND r.id=1", "l.id=0 AND r.id=0", "FALSE"}

var comparisonDistinctForms = []string{" IS DISTINCT FROM ", " IS NOT DISTINCT FROM "}

// An empty pair has no released operator in any of the six slots. Its denials
// differ only by operator text, filter, route and predicate form. Those
// redundant dimensions rotate across a family; the pair itself is never dropped.
func comparisonEmptyPair(left, right int) bool {
	return comparisonSignature(left, right).operators == [6]int{}
}

// Pure, DB-free ordinal: the right's index among this left's empty rights,
// offset by the left index. Independent of subtest selection.
func comparisonEmptyOrdinal(left, right int) int {
	ordinal := left
	for r := 0; r < right; r++ {
		if comparisonEmptyPair(left, r) {
			ordinal++
		}
	}
	return ordinal
}

// The matrix witnesses an empty pair once: one spelling and one filter, both
// direct execution and failed Prepare.
func comparisonMatrixWitness(left, right int) (spelling, filter int) {
	j := comparisonEmptyOrdinal(left, right)
	return j % len(comparisonSpellings), (j / len(comparisonSpellings)) % len(comparisonAnalysisFilters)
}

// PredicateErrors witnesses an empty pair once per DISTINCT and once per
// BETWEEN endpoint, each on a single rotating route.
func comparisonDistinctWitness(left, right int) (form, filter int, prepared bool) {
	j := comparisonEmptyOrdinal(left, right)
	return j % 2, (j / 2) % len(comparisonAnalysisFilters), (j/6)%2 == 1
}

func comparisonBetweenWitness(left, right, endpoint int) (form, filter int, prepared bool) {
	j := comparisonEmptyOrdinal(left, right)
	n := len(comparisonBetweenForms)
	return j % n, (j / n) % len(comparisonAnalysisFilters), (j/(n*len(comparisonAnalysisFilters))+endpoint)%2 == 1
}

// Every family plan must still reach each combination of the rotated
// dimensions; a rotation gap fails before any fixture is allocated.
func comparisonAssertMatrixRotation(t *testing.T, family string) {
	t.Helper()
	covered := map[[2]int]bool{}
	comparisonEachEmptyPair(family, func(left, right int) {
		spelling, filter := comparisonMatrixWitness(left, right)
		covered[[2]int{spelling, filter}] = true
	})
	if want := len(comparisonSpellings) * len(comparisonAnalysisFilters); len(covered) != want {
		t.Fatalf("%s matrix witnesses cover %d spelling/filter combinations, want %d", family, len(covered), want)
	}
}

func comparisonAssertPredicateRotation(t *testing.T, family string) {
	t.Helper()
	distinct, between := map[string]bool{}, map[string]bool{}
	comparisonEachEmptyPair(family, func(left, right int) {
		form, filter, prepared := comparisonDistinctWitness(left, right)
		distinct[fmt.Sprint(form, filter, prepared)] = true
		for endpoint := range 2 {
			form, filter, prepared := comparisonBetweenWitness(left, right, endpoint)
			between[fmt.Sprint(form, endpoint, filter, prepared)] = true
		}
	})
	filters := len(comparisonAnalysisFilters)
	if want := len(comparisonDistinctForms) * filters * 2; len(distinct) != want {
		t.Fatalf("%s DISTINCT witnesses cover %d form/filter/route combinations, want %d", family, len(distinct), want)
	}
	if want := len(comparisonBetweenForms) * 2 * filters * 2; len(between) != want {
		t.Fatalf("%s BETWEEN witnesses cover %d form/endpoint/filter/route combinations, want %d", family, len(between), want)
	}
}

func comparisonEachEmptyPair(family string, visit func(left, right int)) {
	for left, typ := range comparisonConcrete {
		if typ.family != family {
			continue
		}
		for right := range comparisonConcrete {
			if comparisonEmptyPair(left, right) {
				visit(left, right)
			}
		}
	}
}
