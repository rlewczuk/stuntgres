package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func referencePruningScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool), before ...func(*testing.T)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			for _, setup := range before {
				setup(t)
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, prepared) })
			}
		})
	}
}

func referencePruningRangeSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE TABLE pp_range(k INT) PARTITION BY RANGE(k)",
		"CREATE TABLE pp_range_a PARTITION OF pp_range FOR VALUES FROM(1) TO(3)",
		"CREATE TABLE pp_range_b PARTITION OF pp_range FOR VALUES FROM(3) TO(5)",
		"CREATE TABLE pp_range_def PARTITION OF pp_range DEFAULT",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO pp_range VALUES(-1),(0),(1),(1),(2),(3),(4),(5),(NULL)", 9))
}

// All predicates deliberately use bag assertions, including duplicate keys.
// Metadata is checked before iteration even when no row can match.
func referencePruningKeys(run func(fixture.Step), pg bool, query string, keys ...any) {
	rows := make([][]any, len(keys))
	for i, key := range keys {
		rows[i] = []any{key}
	}
	s := fixture.Select(query, []string{"k"}, []string{"INT4"}, rows)
	s.Bag = true
	s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{true})
	run(s)
}
