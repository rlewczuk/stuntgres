package engine

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
)

func insertSelectEvaluationFixture(t *testing.T, sql string) (analyze.Binding, *exec.Source) {
	t.Helper()
	e, q, source := queryCoreFixture(t, sql)
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	e.Mu.Lock()
	relation := e.Catalog.State.MustRelation(ref.Address)
	e.Mu.Unlock()
	return analyze.Binding{Query: q, Relation: relation, Ref: ref, Targets: []int{0}}, source
}
