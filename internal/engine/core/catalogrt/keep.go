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

// ScalarStatsReadSuppressed preserves scalar getter ACL demand while proving
// that no expression is evaluated. Other sources retain their capture policy.
func (q *KeepQuery) ScalarStatsReadSuppressed(leaves []exec.Leaf, i int, mark plan.RowMark) bool {
	if q == nil || mark.Mode != 0 || !sameLeafTables(q.Leaves, leaves) {
		return false
	}
	oid := leaves[i].Relation.Address().ObjectID
	if oid != pgcatalog.StatBGWriterOID && oid != pgcatalog.StatCheckpointerOID {
		return false
	}
	return exec.CatalogSourceFalse(q.Root, i, leaves[i].Ref)
}

// RejectsInput proves that this SELECT cannot demand its single catalog input.
// It mirrors the executor's direct WHERE proof before eager catalog capture;
// unsupported topology keeps ordinary capture and all its demanded errors.
func (q *KeepQuery) RejectsInput(ctx context.Context, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, leaves []exec.Leaf, i int, mark plan.RowMark, locks *exec.LockContext) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if q == nil || len(leaves) != 1 || i != 0 || mark.Mode != 0 || locks != nil || !sameLeafTables(q.Leaves, leaves) {
		return false, nil
	}
	project, ok := q.Root.(*plan.ProjectNode)
	if !ok || project.Frame != 1 || project.CTEOwner || project.RuleInvocation {
		return false, nil
	}
	filter, ok := project.Child.(*plan.FilterNode)
	if !ok {
		return false, nil
	}
	source, ok := filter.Child.(*plan.SourceNode)
	if !ok || source.Occurrence != i || source.Ref != leaves[i].Ref || source.Sample != nil {
		return false, nil
	}
	evaluation := exec.Context{
		EvalFrame:     exec.EvalFrame{Scope: 1, Frame: project.Frame},
		EvalStatement: exec.EvalStatement{Context: ctx, Parameters: q.Parameters, Input: input, Output: output},
	}
	return exec.QueryPredicateRejectsAll(&evaluation, filter.Predicate)
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
