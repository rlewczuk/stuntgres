package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceINETMixedResolution(t *testing.T) {
	p := fixture.NewScenario(t)
	backends := p.Backends()
	for _, b := range backends {
		for _, tc := range []struct {
			q, name string
			rows    [][]any
			bag     bool
		}{
			{"VALUES ('10'::cidr),('192.168.0.1/24'::inet)", "column1", [][]any{{"10.0.0.0/8"}, {"192.168.0.1/24"}}, true},
			{"VALUES ('192.168.0.1/24'::inet),('10'::cidr)", "column1", [][]any{{"192.168.0.1/24"}, {"10.0.0.0/8"}}, true},
			{"SELECT COALESCE(NULL::cidr,'192.168.0.1/24'::inet) AS v", "v", [][]any{{"192.168.0.1/24"}}, false},
			{"SELECT COALESCE(NULL::inet,'10'::cidr) AS v", "v", [][]any{{"10.0.0.0/8"}}, false},
			{"SELECT NULLIF('10'::cidr,'10.0.0.0/8'::inet) AS v", "v", [][]any{{nil}}, false},
			{"SELECT NULLIF('10.0.0.0/8'::inet,'10'::cidr) AS v", "v", [][]any{{nil}}, false},
			{"SELECT NULLIF('10'::cidr,'11.0.0.0/8'::inet) AS v", "v", [][]any{{"10.0.0.0/8"}}, false},
			{"SELECT NULLIF('192.168.0.1/24'::inet,'192.168/24'::cidr) AS v", "v", [][]any{{"192.168.0.1/24"}}, false},
		} {
			s := fixture.Select(tc.q, []string{tc.name}, []string{"INET"}, tc.rows)
			s.Bag = tc.bag
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { inetMetadata(t, r, b.PG, []string{tc.name}, []string{"INET"}, nil) }
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
	}
}
