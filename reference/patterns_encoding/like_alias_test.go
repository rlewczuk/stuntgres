package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Root's pinned18.0 alias probes independently established these labels,
// physical INT4 values and successful expression-versus-label selection.
func TestReferenceLikeContextualLabels(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(x INTEGER)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1)", 1))
	for _, s := range []fixture.Step{
		fixture.Select("SELECT x like FROM t", []string{"like"}, []string{"INT4"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT x ilike FROM t", []string{"ilike"}, []string{"INT4"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT x+1 like FROM t;", []string{"like"}, []string{"INT4"}, [][]any{{int64(2)}}),
		fixture.Select("SELECT x+1 ilike FROM t", []string{"ilike"}, []string{"INT4"}, [][]any{{int64(2)}}),
		fixture.Select("SELECT x ilike,x FROM t", []string{"ilike", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}),
		fixture.Select("SELECT x AS ilike FROM t", []string{"ilike"}, []string{"INT4"}, [][]any{{int64(1)}}),
	} {
		s.CheckMetadata = likeReferenceMetadata
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
}
