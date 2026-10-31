package catalogrt

import (
	"context"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// KeepQuery is the bound SELECT whose own leaves are being captured: its
// root's occurrences index exactly these leaves.
type KeepQuery struct {
	Root       plan.Node
	Leaves     []exec.Leaf
	Parameters []sqltype.Value
}

// Keep returns the producer row test for leaf i of leaves, or the zero Keep.
// q is nil outside a capturing SELECT. Only system relations with
// pgcatalog.KeyColumns use one, and only for the leaves of the SELECT being
// captured: nested view and policy captures number their occurrences
// separately. locks is the capture's lock context.
func (q *KeepQuery) Keep(ctx context.Context, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, leaves []exec.Leaf, i int, mark plan.RowMark, locks *exec.LockContext) pgcatalog.Keep {
	leaf := leaves[i]
	if q == nil || mark.Mode != 0 || !sameLeafTables(q.Leaves, leaves) {
		return pgcatalog.Keep{}
	}
	for _, column := range pgcatalog.KeyColumns(leaf.Relation.Address().ObjectID) {
		if test := exec.CatalogSourceKeep(ctx, q.Parameters, input, output, q.Root, locks == nil, i, leaf.Ref, column); test != nil {
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
