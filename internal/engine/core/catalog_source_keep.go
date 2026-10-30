package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// catalogKeepQuery is the bound SELECT whose own leaves are being captured:
// its root's occurrences index exactly these leaves.
type catalogKeepQuery struct {
	root       plan.Node
	leaves     []exec.Leaf
	parameters []sqltype.Value
}

// captureSelectSourcesLocked is CaptureQuerySourcesLocked for a SELECT whose
// root reads the leaves. While it runs, a pg_settings, pg_attribute, pg_class,
// pg_proc or information_schema routines/parameters leaf filtered by one of its
// key columns builds only the rows the filter can accept.
func (e *Engine) captureSelectSourcesLocked(scope *StatementScope, root plan.Node, leaves []exec.Leaf, parameters []sqltype.Value, singleton bool) (*exec.Source, error) {
	previous := scope.catalogKeep
	scope.catalogKeep = &catalogKeepQuery{root: root, leaves: leaves, parameters: parameters}
	defer func() { scope.catalogKeep = previous }()
	return e.CaptureQuerySourcesLocked(scope, leaves, singleton)
}

// catalogSourceKeep returns the producer row test for leaf i of leaves, or the
// zero Keep. Only system relations with pgcatalog.KeyColumns use one, and only
// for the leaves of the SELECT being captured: nested view and policy captures
// number their occurrences separately. locks is the capture's lock context.
func (s *StatementScope) catalogSourceKeep(leaves []exec.Leaf, i int, mark plan.RowMark, locks *exec.LockContext) pgcatalog.Keep {
	q := s.catalogKeep
	leaf := leaves[i]
	if q == nil || mark.Mode != 0 || !sameLeafTables(q.leaves, leaves) {
		return pgcatalog.Keep{}
	}
	for _, column := range pgcatalog.KeyColumns(leaf.Relation.Address().ObjectID) {
		if test := exec.CatalogSourceKeep(s.Op.Context(), q.parameters, s.Input, s.Output, q.root, locks == nil, i, leaf.Ref, column); test != nil {
			return pgcatalog.Keep{Column: column, Test: test}
		}
	}
	return pgcatalog.Keep{}
}

func sameLeafTables(a, b []exec.Leaf) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Table != b[i].Table || a[i].Ref != b[i].Ref {
			return false
		}
	}
	return true
}
