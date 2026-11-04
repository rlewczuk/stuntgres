package catalogrt

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
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

// ScalarStatsReadSuppressed proves literal non-evaluation for this occurrence.
// STABLE consumers retain getter ACL demand; the xact-function consumer instead
// eliminates uninitialized volatile-subtree demand. Other capture policy is unchanged.
func (q *KeepQuery) ScalarStatsReadSuppressed(leaves []exec.Leaf, i int, mark plan.RowMark) bool {
	if q == nil || mark.Mode != 0 || !sameLeafTables(q.Leaves, leaves) {
		return false
	}
	oid := leaves[i].Relation.Address().ObjectID
	if oid != pgcatalog.StatBGWriterOID && oid != pgcatalog.StatCheckpointerOID && oid != pgcatalog.StatXactUserFunctionsOID {
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
	rejected, err := exec.QueryPredicateRejectsAll(&evaluation, filter.Predicate)
	if rejected || err != nil {
		return rejected, err
	}
	// Only this source owns the shallow name-key conjunction contract. The
	// counterpart must be a non-failing builtin equality, not arbitrary work
	// whose errors or effects a false Boolean operand could otherwise hide.
	leaf := leaves[i]
	if leaf.Relation == nil || leaf.Relation != q.Leaves[i].Relation ||
		leaf.Relation.RelationKind != catalog.CatalogRelation ||
		leaf.Ref.Address != leaf.Relation.Address() || leaf.Ref.Address.ObjectID != pgcatalog.StatSubscriptionStatsOID {
		return false, nil
	}
	logical, ok := filter.Predicate.(plan.LogicalExpr)
	if !ok || logical.Op != parser.LogicalAnd || logical.QueryRight {
		return false, nil
	}
	equality, rejection := logical.Left, logical.Right
	if _, ok := equality.(plan.ComparisonExpr); !ok {
		equality, rejection = rejection, equality
	}
	safe, err := subscriptionStatsNameEquality(&evaluation, equality, leaf)
	if !safe || err != nil || rejection == nil {
		return false, err
	}
	usage := rejection.ResultUsage()
	if usage.Type == nil || !usage.Type.IsBuiltin() || usage.Type.ID() != sqltype.Bool || usage.Type.IsDomain() || usage != usage.Type.Usage() {
		return false, nil
	}
	value, known, err := plan.QueryPredicateProofValue(&evaluation, rejection)
	if err != nil || !known || value.Type() != usage.Type {
		return false, err
	}
	truth, err := plan.TruthOf(value)
	return err == nil && truth != plan.True, err
}

// subscriptionStatsNameEquality checks structure and the current imported
// probe only. It never evaluates a column, comparison, cast or routine.
func subscriptionStatsNameEquality(c *exec.Context, bound expr.Expr, leaf exec.Leaf) (bool, error) {
	equality, ok := bound.(plan.ComparisonExpr)
	if !ok || equality.Operator != "=" || equality.Named || equality.RoutineRef != (ids.ObjectRef{}) ||
		equality.RoutineRecipeReference != nil || equality.Namespace != "" && equality.Namespace != "pg_catalog" {
		return false, nil
	}
	if equality.RoutineReference != nil {
		procedure, ok := equality.RoutineReference.(*catalog.Procedure)
		declaration := routine.OperatorRoutine("=", ids.OID(sqltype.Name), ids.OID(sqltype.Name))
		if !ok || procedure == nil || declaration == nil || procedure.Builtin != declaration ||
			procedure.Address() != (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID}) ||
			procedure.Volatility != expr.Immutable || !procedure.Strict || procedure.Implementation != nil ||
			procedure.SQL != nil || procedure.SQLBody != nil || procedure.Host != nil || procedure.HostHandler != nil {
			return false, nil
		}
	}
	properties := equality.Properties()
	if properties.Volatility != expr.Immutable || properties.StageDependent || properties.DomainDependent || properties.RuntimeDomainConversion {
		return false, nil
	}
	column, ok := equality.Left.(plan.ColumnExpr)
	probe := equality.Right
	if !ok {
		column, ok = equality.Right.(plan.ColumnExpr)
		probe = equality.Left
	}
	slot := pgcatalog.StatSubscriptionStatsNameColumn
	origin := column.Origin
	attribute := origin.Address.SubID
	origin.Address.SubID = 0
	if !ok || column.Slot != slot || column.Virtual != nil || column.MergeSource || column.RowWidth != 0 ||
		column.Scope != c.Scope || column.Frame != c.Frame || column.QueryOccurrence != 0 ||
		origin != leaf.Ref || slot >= len(leaf.Relation.Columns) || slot >= len(leaf.Relation.Attributes) ||
		attribute != leaf.Relation.Attributes[slot] || probe == nil {
		return false, nil
	}
	usage := column.ResultUsage()
	if usage != leaf.Relation.Columns[slot].Usage || usage.Type == nil || !usage.Type.IsBuiltin() ||
		usage.Type.IsDomain() || usage.Type.ID() != sqltype.Name || probe.ResultUsage() != usage ||
		!equality.Predicate.EqualityFor(usage) {
		return false, nil
	}
	value, known, err := plan.QueryPredicateProofValue(c, probe)
	return known && err == nil && value.Type() == usage.Type, err
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
