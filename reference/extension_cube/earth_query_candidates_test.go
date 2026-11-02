package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthQueryCandidates(t *testing.T) {
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		earthQueryCandidateSetup(t, p, b, c)
		earthQueryCandidateVerify(t, p, b, c)
		// A real zero-distance boundary, not a decimal angle assumed to be an
		// exactly representable 100000-metre great-circle distance.
		q := "SELECT earth_box('(6378168)'::earth,0) @> '(6378168)'::earth AS candidate,earth_distance('(6378168)'::earth,'(6378168,0,0)'::earth)<=0 AS exact"
		earthQueryRoutes(t, p, b, c, fixture.Select(q, []string{"candidate", "exact"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		// Literal calls exercise the same independently specified candidate bag.
		for _, tc := range []struct {
			predicate string
			want      [][]any
		}{
			{"earth_box(ll_to_earth(0,0),100000) @> p", [][]any{{int64(1)}, {int64(2)}, {int64(3)}}},
			{"p <@ earth_box(ll_to_earth(0,0),100000) AND earth_distance(ll_to_earth(0,0),p)<=100000", [][]any{{int64(1)}, {int64(2)}}},
		} {
			q := "SELECT id FROM earth_query_candidates WHERE " + tc.predicate + " ORDER BY id"
			earthQueryRoutes(t, p, b, c, fixture.Select(q, []string{"id"}, []string{"INT4"}, tc.want))
		}
	})
}
