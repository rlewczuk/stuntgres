package reference_test

import "testing"

// Root's pinned18.0 alias probes independently established these labels,
// physical INT4 values and successful expression-versus-label selection.
func TestReferenceLikeContextualLabels(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(x INTEGER)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1)", 1))
	for _, s := range []referenceStep{
		referenceSelect("SELECT x like FROM t", []string{"like"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT x ilike FROM t", []string{"ilike"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT x+1 like FROM t;", []string{"like"}, []string{"INT4"}, [][]any{{int64(2)}}),
		referenceSelect("SELECT x+1 ilike FROM t", []string{"ilike"}, []string{"INT4"}, [][]any{{int64(2)}}),
		referenceSelect("SELECT x ilike,x FROM t", []string{"ilike", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}),
		referenceSelect("SELECT x AS ilike FROM t", []string{"ilike"}, []string{"INT4"}, [][]any{{int64(1)}}),
	} {
		s.checkMetadata = likeReferenceMetadata
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}
