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
// once, under a filter whose first-evaluated conjunct compares column slot of
// that scan, directly or through non-volatile coercions that check no domain
// (such as an information_schema domain column's relabel to its base type),
// with non-NULL statement constants (text, or an oid probe such as a
// bound 'name'::regclass literal): "=" or "= ANY" of a list, with a builtin
// pg_catalog comparison. The test evaluates that comparison's own predicate
// and rejects a cell only when the conjunct is FALSE: the filter then rejects
// the row without evaluating anything else. TRUE, NULL and a failing
// evaluation keep the cell for the filter. nil means no such filter, and
// every row must be produced.
//
// The filter is directly over the scan, or over a chain of inner and LEFT
// joins whose left inputs lead to it. Through joins the conjunct must be one
// JoinRestriction moves below each of them, and only when the statement reads
// without a lock context (lockFree) or identity reads: execution then filters
// the scan's rows before any join pairs them, so a skipped row never reaches
// a join predicate or a correlated right input either.
//
// parameters, input and output are the statement's, as the filter sees them
// at the top-level scope and frame.
func CatalogSourceKeep(ctx context.Context, parameters []sqltype.Value, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, root plan.Node, lockFree bool, occurrence int, ref ids.ObjectRef, slot int) func(sqltype.Value) bool {
	var scan *plan.SourceNode
	var filter *plan.FilterNode
	var joins []*plan.JoinNode
	scans := 0
	plan.VisitCTEQuery(root, func(n plan.Node) {
		switch n := n.(type) {
		case *plan.SourceNode:
			if n.Occurrence == occurrence {
				scan, scans = n, scans+1
			}
		case *plan.FilterNode:
			var path []*plan.JoinNode
			child := n.Child
			for {
				join, ok := child.(*plan.JoinNode)
				if !ok {
					break
				}
				path, child = append(path, join), join.Left
			}
			if leaf, ok := child.(*plan.SourceNode); ok && leaf.Occurrence == occurrence {
				filter, joins = n, path
			}
		}
	}, func(expr.Expr) {}, true)
	if scans != 1 || filter == nil || scan.Ref != ref {
		return nil
	}
	conjunct := firstConjunct(filter.Predicate)
	if len(joins) != 0 && (!lockFree || QueryHasIdentityReads(root) || !restrictedThrough(joins, conjunct)) {
		return nil
	}
	evaluation := Context{EvalFrame: EvalFrame{Scope: 1, Frame: 1}, EvalStatement: EvalStatement{Parameters: parameters, Input: input, Output: output}}
	evaluation.setContext(ctx)
	return catalogColumnKeep(&evaluation, conjunct, occurrence, ref, slot)
}

// restrictedThrough reports whether JoinRestriction moves conjunct below each
// join, outermost first, down to the leftmost input.
func restrictedThrough(joins []*plan.JoinNode, conjunct expr.Expr) bool {
	for _, join := range joins {
		if join.Kind != parser.JoinInner && join.Kind != parser.JoinLeft || len(join.Merges) != 0 || !plan.JoinLeftRestriction(join, conjunct) {
			return false
		}
	}
	return true
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
	// operand returns the comparison operand a key cell yields, or nil when x
	// is not the key column. The column may sit under coercions, such as an
	// information_schema domain column's relabel to its base type; the chain
	// is evaluated with the cell in the column's place, and a failure keeps
	// the row.
	operand := func(x expr.Expr) func(sqltype.Value) (sqltype.Value, bool) {
		if isColumn(x) {
			return func(cell sqltype.Value) (sqltype.Value, bool) { return cell, true }
		}
		leaf := &keyCell{}
		chain, ok := keyCoercions(x, isColumn, leaf)
		if !ok {
			return nil
		}
		return func(cell sqltype.Value) (sqltype.Value, bool) {
			leaf.cell = cell
			v, err := chain.Eval(evaluation)
			return v, err == nil
		}
	}
	switch c := e.(type) {
	case plan.ComparisonExpr:
		if c.Operator != "=" || !builtin(c.RoutineRef, c.Namespace, c.Properties()) {
			return nil
		}
		if key := operand(c.Left); key != nil {
			probe, ok := constant(c.Right, c.Left)
			if !ok {
				return nil
			}
			return func(cell sqltype.Value) bool {
				v, ok := key(cell)
				return !ok || truth(c.Predicate, v, probe)
			}
		}
		if key := operand(c.Right); key != nil {
			probe, ok := constant(c.Left, c.Right)
			if !ok {
				return nil
			}
			return func(cell sqltype.Value) bool {
				v, ok := key(cell)
				return !ok || truth(c.Predicate, probe, v)
			}
		}
	case plan.ScalarArrayComparisonExpr:
		if c.Operator != "=" || c.All || c.Array != nil || len(c.Items) == 0 || !builtin(c.RoutineRef, c.Namespace, c.Properties()) {
			return nil
		}
		key := operand(c.Left)
		if key == nil {
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
			v, ok := key(cell)
			if !ok {
				return true
			}
			for _, probe := range probes {
				if truth(c.Predicate, v, probe) {
					return true
				}
			}
			return false
		}
	}
	return nil
}

// keyCell stands for the key column in a coercion chain over it: it
// evaluates to the cell under test.
type keyCell struct {
	expr.Info
	cell sqltype.Value
}

func (k *keyCell) Eval(expr.Context) (sqltype.Value, error) { return k.cell, nil }

// keyCoercions returns x, a chain of coercions over the key column, with leaf
// in the column's place. Like a probe's coercions (statementConstant), each
// must be non-volatile and check no domain, so that it depends on the cell
// alone.
func keyCoercions(x expr.Expr, isColumn func(expr.Expr) bool, leaf *keyCell) (expr.Expr, bool) {
	c, ok := x.(plan.CoercionExpr)
	if !ok {
		return nil, false
	}
	p := c.Properties()
	if c.Plan.ChecksDomain() || p.Volatility == expr.Volatile || p.StageDependent || p.DomainDependent || p.RuntimeDomainConversion {
		return nil, false
	}
	if column, ok := c.Child.(plan.ColumnExpr); ok && isColumn(column) {
		leaf.Info = column.Info
		c.Child = leaf
		return c, true
	}
	child, ok := keyCoercions(c.Child, isColumn, leaf)
	if !ok {
		return nil, false
	}
	c.Child = child
	return c, true
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
// chain is an oid, regclass or routine alias, ending in an oid literal or
// parameter, in a regclass or regproc/regprocedure literal, or in an integer
// literal cast to oid. The binder resolves 'name'::regclass and
// 'f(x)'::regprocedure literals to their OID (bindRegclass, RoutineConstant),
// so no step reads the input environment; a text cast to regclass would, and
// is rejected.
func oidConstant(x expr.Expr) bool {
	for {
		typ := x.ResultUsage().Type
		if typ == nil {
			return false
		}
		id := typ.ID()
		if id != sqltype.Oid && id != sqltype.Regclass && !sqltype.IsRoutineAlias(id) {
			return false
		}
		switch c := x.(type) {
		case plan.CoercionExpr:
			if id == sqltype.Oid && integerLiteral(c.Child) {
				return true
			}
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

// integerLiteral reports an int2, int4 or int8 literal, such as 16384 in
// oid = 16384.
func integerLiteral(x expr.Expr) bool {
	literal, ok := x.(plan.LiteralExpr)
	if !ok || literal.ResultUsage().Type == nil {
		return false
	}
	switch literal.ResultUsage().Type.ID() {
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		return true
	}
	return false
}
