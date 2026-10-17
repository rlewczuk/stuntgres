package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceComparisonBatchSelection(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			expr string
			want any
		}{
			{"'a'::name IN ('a'::varchar,'b'::varchar)", true},
			{"'a '::varchar IN ('a'::bpchar,'b'::bpchar)", false},
			{"1::int2 IN (2::numeric,1::int8,NULL)", true},
			{"1::int8 NOT IN (2::numeric,NULL)", nil},
			{"TIME '01:00' IN (INTERVAL '1 hour')", true},
			// transformAExprIn requests select_common_type with NULL context:
			// failure selects pairwise OR, not an IN common-type diagnostic.
			{"TIME '01:00' IN (INTERVAL '1 hour',INTERVAL '2 hours')", true},
			{"INTERVAL '1 hour' IN (TIME '01:00',TIME '02:00')", true},
			{"'1'::xid IN (1::integer,2::integer)", true},
		} {
			comparisonCheckpointBool(t, p, b, tc.expr, tc.want)
		}
	})
}
