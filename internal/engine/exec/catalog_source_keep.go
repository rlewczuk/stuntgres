package exec

import (
	"context"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CatalogSourceKeep returns a test of one column cell of the catalog source
// that leaf occurrence of root scans, so its producer can skip rows the
// statement's filter rejects. It applies only when root scans the occurrence
// once, directly under a filter whose first-evaluated conjunct compares column
// slot of that scan with non-NULL statement constants (text, or an oid probe
// such as a bound 'name'::regclass literal): "=" or "= ANY" of a list, with a
// builtin pg_catalog comparison. The test evaluates that
// comparison's own predicate and rejects a cell only when the conjunct is
// FALSE: the filter then rejects the row without evaluating anything else.
// TRUE, NULL and a failing evaluation keep the cell for the filter. nil means
// no such filter, and every row must be produced.
//
// parameters, input and output are the statement's, as the filter sees them
// at the top-level scope and frame.
func CatalogSourceKeep(ctx context.Context, parameters []sqltype.Value, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, root plan.Node, occurrence int, ref ids.ObjectRef, slot int) func(sqltype.Value) bool {
	var scan *plan.SourceNode
	var filter *plan.FilterNode
	scans := 0
	plan.VisitCTEQuery(root, func(n plan.Node) {
		switch n := n.(type) {
		case *plan.SourceNode:
			if n.Occurrence == occurrence {
				scan, scans = n, scans+1
			}
		case *plan.FilterNode:
			if child, ok := n.Child.(*plan.SourceNode); ok && child.Occurrence == occurrence {
				filter = n
			}
		}
	}, func(expr.Expr) {}, true)
	if scans != 1 || filter == nil || filter.Child != plan.Node(scan) || scan.Ref != ref {
		return nil
	}
	evaluation := Context{EvalFrame: EvalFrame{Scope: 1, Frame: 1}, EvalStatement: EvalStatement{Parameters: parameters, Input: input, Output: output}}
	evaluation.setContext(ctx)
	return catalogColumnKeep(&evaluation, firstConjunct(filter.Predicate), occurrence, ref, slot)
}

// firstConjunct returns the leftmost operand of an AND chain, which AND
// evaluates first and which makes the whole predicate FALSE when it is FALSE.
func firstConjunct(e expr.Expr) expr.Expr {
	for {
		logical, ok := e.(plan.LogicalExpr)
		if !ok || logical.Op != parser.LogicalAnd {
			return e
		}
		e = logical.Left
	}
}

// catalogColumnKeep builds the test for one conjunct, or returns nil.
func catalogColumnKeep(evaluation *Context, e expr.Expr, occurrence int, ref ids.ObjectRef, slot int) func(sqltype.Value) bool {
	isColumn := func(x expr.Expr) bool {
		c, ok := x.(plan.ColumnExpr)
		return ok && c.Slot == slot && c.Virtual == nil && !c.MergeSource && c.Scope == evaluation.Scope && c.Frame == evaluation.Frame &&
			(c.QueryOccurrence == 0 || c.QueryOccurrence == occurrence+1) &&
			// Origin's SubID names the attribute; Slot already selects it.
			c.Origin.Domain() == ref.Domain() && c.Origin.Epoch == ref.Epoch && c.Origin.Address.ClassID == ref.Address.ClassID && c.Origin.Address.ObjectID == ref.Address.ObjectID
	}
	builtin := func(routine ids.ObjectRef, namespace string, properties expr.Properties) bool {
		return routine == (ids.ObjectRef{}) && (namespace == "" || namespace == "pg_catalog") && properties.Volatility != expr.Volatile
	}
	// A probe is a non-NULL text or oid constant of the column's own type: its
	// value cannot depend on the input or output environment the filter sees.
	constant := func(x expr.Expr, column expr.Expr) (sqltype.Value, bool) {
		if !statementConstant(x) || !textConstant(x) && !oidConstant(x) || x.ResultUsage() != column.ResultUsage() {
			return sqltype.Value{}, false
		}
		v, err := x.Eval(evaluation)
		return v, err == nil && !v.IsNull()
	}
	// false reports only a definitely FALSE comparison.
	truth := func(predicate sqltype.ComparisonPredicate, left, right sqltype.Value) bool {
		result, err := predicate.Evaluate(evaluation.FunctionContext(), left, right, evaluation.FunctionInput())
		if err != nil {
			return true
		}
		t, err := plan.TruthOf(result)
		return err != nil || t != plan.False
	}
	switch c := e.(type) {
	case plan.ComparisonExpr:
		if c.Operator != "=" || !builtin(c.RoutineRef, c.Namespace, c.Properties()) {
			return nil
		}
		if isColumn(c.Left) {
			probe, ok := constant(c.Right, c.Left)
			if !ok {
				return nil
			}
			return func(cell sqltype.Value) bool { return truth(c.Predicate, cell, probe) }
		}
		if isColumn(c.Right) {
			probe, ok := constant(c.Left, c.Right)
			if !ok {
				return nil
			}
			return func(cell sqltype.Value) bool { return truth(c.Predicate, probe, cell) }
		}
	case plan.ScalarArrayComparisonExpr:
		if c.Operator != "=" || c.All || c.Array != nil || len(c.Items) == 0 || !builtin(c.RoutineRef, c.Namespace, c.Properties()) || !isColumn(c.Left) {
			return nil
		}
		probes := make([]sqltype.Value, len(c.Items))
		for i, item := range c.Items {
			probe, ok := constant(item, c.Left)
			if !ok {
				return nil
			}
			probes[i] = probe
		}
		return func(cell sqltype.Value) bool {
			for _, probe := range probes {
				if truth(c.Predicate, cell, probe) {
					return true
				}
			}
			return false
		}
	}
	return nil
}

// textConstant reports whether every expression of a statement constant's
// literal, parameter and coercion chain has a text-family type.
func textConstant(x expr.Expr) bool {
	for {
		typ := x.ResultUsage().Type
		if typ == nil {
			return false
		}
		switch typ.ID() {
		case sqltype.Text, sqltype.VarChar, sqltype.Name, sqltype.BPChar, sqltype.Unknown:
		default:
			return false
		}
		coercion, ok := x.(plan.CoercionExpr)
		if !ok {
			return true
		}
		x = coercion.Child
	}
}

// oidConstant reports whether every expression of a statement constant's
// chain is an oid or regclass, ending in an oid literal or parameter or in a
// regclass literal. The binder resolves 'name'::regclass literals to their OID
// (bindRegclass), so no step reads the input environment; a text cast to
// regclass would, and is rejected.
func oidConstant(x expr.Expr) bool {
	for {
		typ := x.ResultUsage().Type
		if typ == nil {
			return false
		}
		id := typ.ID()
		if id != sqltype.Oid && id != sqltype.Regclass {
			return false
		}
		switch c := x.(type) {
		case plan.CoercionExpr:
			x = c.Child
		case plan.LiteralExpr:
			return true
		case plan.ParameterExpr:
			return id == sqltype.Oid
		default:
			return false
		}
	}
}
