package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 parse_expr.c:transformAExprBetween lowers each comparison
// independently. Explicit ASYMMETRIC is the default, not sorted endpoints.
func TestReferenceComparisonPredicateSyntax(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, tc := range []struct {
				name, expr string
				want       any
			}{
				{"included_lower", "1 BETWEEN ASYMMETRIC 1 AND 2", true},
				{"included_upper", "2 BETWEEN ASYMMETRIC 1 AND 2", true},
				{"outside", "3 BETWEEN ASYMMETRIC 1 AND 2", false},
				{"reversed", "1 BETWEEN ASYMMETRIC 2 AND 1", false},
				{"negated_reversed", "1 NOT BETWEEN ASYMMETRIC 2 AND 1", true},
				{"negated_inside", "1 NOT BETWEEN ASYMMETRIC 1 AND 2", false},
				{"null_value", "NULL::int4 BETWEEN ASYMMETRIC 1 AND 2", nil},
				{"null_lower", "1 BETWEEN ASYMMETRIC NULL::int4 AND 2", nil},
				{"null_upper_decisive", "0 BETWEEN ASYMMETRIC 1 AND NULL::int4", false},
				{"negated_null_decisive", "0 NOT BETWEEN ASYMMETRIC 1 AND NULL::int4", true},
				{"negated_null", "1 NOT BETWEEN ASYMMETRIC NULL::int4 AND 2", nil},
				{"nested_mixed", "2::int2 BETWEEN ASYMMETRIC (1::int4 + 1) AND 3::int8", true},
				{"parameter", "$1::int4 BETWEEN ASYMMETRIC 1::int2 AND 3::int8", true},
				{"symmetric_control", "1 BETWEEN SYMMETRIC 2 AND 1", true},
				{"default_control", "1 BETWEEN 2 AND 1", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if tc.name == "parameter" {
						comparisonCheckpointBool(t, p, b, tc.expr, tc.want, 2)
					} else {
						comparisonCheckpointBool(t, p, b, tc.expr, tc.want)
					}
				})
			}
		})
	}
}
