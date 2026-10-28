package partition

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func (p *prover) classInput(at int) sqltype.ID {
	class, ok := p.view.OperatorClass(ids.OID(p.entries[at].Class.OID()))
	if !ok {
		return 0
	}
	input, ok := p.view.LookupType(sqltype.ID(class.InputType))
	if !ok {
		return 0
	}
	if !input.Concrete() {
		return p.entries[at].Usage.Type.ID()
	}
	return input.ID()
}

// Only the exact qualification's binary/identity adapter may be peeled. No
// sizing, domain entry, function/IO/array cast or unknown-input authority passes.
func (p *prover) binaryChild(node expr.Expr, at int) (expr.Expr, bool) {
	if node == nil {
		return nil, false
	}
	target := node.ResultUsage()
	if target.Type == nil || target.Type.ID() != p.classInput(at) || target.Typmod >= 0 || target.Type.IsDomain() {
		return nil, false
	}
	var child expr.Expr
	switch n := node.(type) {
	case plan.DatumCastExpr:
		child = n.Child
		if child == nil || n.Method != "b" || n.Context != "i" || n.Size {
			return nil, false
		}
		if _, ok := catalog.ImplicitBinaryCast(p.view, child.ResultUsage().Type.ID(), target.Type.ID()); !ok {
			return nil, false
		}
		binary, err := sqltype.PlanBinaryCoercion(child.ResultUsage().Type, target.Type)
		if err != nil || binary != n.Binary {
			return nil, false
		}
	case plan.CoercionExpr:
		child = n.Child
		if child == nil || n.UnknownInputCast != nil || n.Plan.ChecksDomain() {
			return nil, false
		}
		if _, ok := catalog.ImplicitBinaryCast(p.view, child.ResultUsage().Type.ID(), target.Type.ID()); !ok {
			return nil, false
		}
		fresh, err := sqltype.PlanCoercion(child.ResultUsage(), target, n.Mode)
		valid := err == nil && fresh.Equal(n.Plan) && !fresh.ChecksDomain()
		if !valid {
			fresh, err = sqltype.PlanDeclaredBinaryCoercion(child.ResultUsage(), target)
			valid = err == nil && fresh.Equal(n.Plan) && !fresh.ChecksDomain()
		}
		if !valid {
			return nil, false
		}
	default:
		return nil, false
	}
	if child.ResultUsage().Collation.ID != target.Collation.ID {
		return nil, false
	}
	return child, true
}

func (p *prover) sameKey(node expr.Expr, at int) bool {
	if node == nil {
		return false
	}
	key := p.keys[at]
	if proofSame(node, key) {
		return true
	}
	if n, ok := node.(plan.CollationExpr); ok {
		if k, ok := key.(plan.CollationExpr); ok && n.Usage == k.Usage {
			if proofSame(n.Child, k.Child) {
				return true
			}
			if child, ok := p.binaryChild(n.Child, at); ok {
				return proofSame(child, k.Child)
			}
		}
		if n.Usage.Collation.ID == key.ResultUsage().Collation.ID {
			node = n.Child
		}
	}
	if child, ok := p.binaryChild(node, at); ok {
		return proofSame(child, key)
	}
	return false
}

func (p *prover) comparisonLiteral(node expr.Expr, at int) (plan.LiteralExpr, bool, error) {
	if child, ok := p.binaryChild(node, at); ok {
		return p.comparisonLiteral(child, at)
	}
	literal, ok := node.(plan.LiteralExpr)
	if !ok || literal.Value.IsNull() {
		return plan.LiteralExpr{}, false, nil
	}
	expected := p.entries[at].Usage.Type
	if literal.Value.Type() == expected {
		return literal, true, nil
	}
	if literal.Value.Type().ID() != p.classInput(at) {
		return plan.LiteralExpr{}, false, nil
	}
	// Re-express a concrete family-input literal in the saved key's datum domain
	// only through the inverse physical mapping of its admitted binary adapter.
	if _, ok := catalog.ImplicitBinaryCast(p.view, expected.ID(), literal.Value.Type().ID()); !ok || expected.IsDomain() {
		return plan.LiteralExpr{}, false, nil
	}
	binary, err := sqltype.PlanBinaryCoercion(literal.Value.Type(), expected)
	if err != nil {
		return plan.LiteralExpr{}, false, nil
	}
	value, err := binary.Apply(p.ctx, literal.Value)
	if err != nil {
		return plan.LiteralExpr{}, false, err
	}
	literal.Value = value
	literal.Info.Usage.Type = expected
	return literal, true, nil
}

func comparisonOperation(operator string) (sqltype.ComparisonOperator, bool) {
	switch operator {
	case "<":
		return sqltype.CompareLess, true
	case "<=":
		return sqltype.CompareLessEqual, true
	case "=":
		return sqltype.CompareEqual, true
	case ">=":
		return sqltype.CompareGreaterEqual, true
	case ">":
		return sqltype.CompareGreater, true
	}
	return 0, false
}

func (p *prover) comparison(node expr.Expr, at int) (expr.Expr, expr.Expr, string, bool) {
	entry := p.entries[at]
	var left, right expr.Expr
	var selected ids.ObjectAddress
	var procedure *catalog.Procedure
	var predicate sqltype.ComparisonPredicate
	var pattern, hasPredicate bool
	switch n := node.(type) {
	case plan.SelectedOperatorExpr:
		current, ok := p.view.Operator(n.Ref.Address)
		if !ok || current != n.Operator || n.Ref.Epoch != p.view.Epoch() || n.Ref.Domain() != p.view.Owner().Domain {
			return nil, nil, "", false
		}
		selected = n.Ref.Address
		count := 0
		plan.ExpressionChildren(n.Provider, func(arg expr.Expr) {
			if count == 0 {
				left = arg
			}
			if count == 1 {
				right = arg
			}
			count++
		})
		if count != 2 {
			return nil, nil, "", false
		}
	case functions.TextPatternComparison:
		left, right = n.Args[0], n.Args[1]
		procedure, _ = n.RoutineReference.(*catalog.Procedure)
		if procedure == nil {
			return nil, nil, "", false
		}
		predicate, hasPredicate, pattern = n.Predicate, true, true
	case plan.ComparisonExpr:
		left, right = n.Left, n.Right
		procedure, _ = n.RoutineReference.(*catalog.Procedure)
		predicate, hasPredicate = n.Predicate, true
		if left == nil || right == nil {
			return nil, nil, "", false
		}
		if procedure == nil {
			// Retain the semantic default-provider fast path, not a nil-authority bypass.
			class, ok := p.view.UniqueDefaultIndexClass(403, entry.Usage.Type)
			if !ok {
				return nil, nil, "", false
			}
			ordering, err := routine.BindIndexOrdering(p.view, entry.Usage, class)
			if err != nil || !ordering.SameOperation(entry.Comparison) {
				return nil, nil, "", false
			}
			op, ok := comparisonOperation(n.Operator)
			if !ok {
				return nil, nil, "", false
			}
			canonical, err := sqltype.BindComparisonPredicate(op, left.ResultUsage().Type, right.ResultUsage().Type, n.ResultUsage().Type, entry.Usage.Collation, false)
			if err != nil || !canonical.Equal(predicate) {
				return nil, nil, "", false
			}
			collation, indeterminate := predicate.InputCollation()
			return left, right, n.Operator, !indeterminate && collation.ID == entry.Usage.Collation.ID
		}
	default:
		return nil, nil, "", false
	}
	if left == nil || right == nil {
		return nil, nil, "", false
	}
	if procedure != nil {
		current, ok := p.view.Procedure(procedure.Address())
		if !ok || current != procedure {
			return nil, nil, "", false
		}
	}
	ordering, err := routine.BindIndexOrdering(p.view, entry.Usage, entry.Class)
	if err != nil || !ordering.SameOperation(entry.Comparison) {
		return nil, nil, "", false
	}
	for strategy := int16(1); strategy <= 5; strategy++ {
		op, err := BoundOperator(p.view, entry, strategy)
		if err != nil {
			return nil, nil, "", false
		}
		if selected != (ids.ObjectAddress{}) && selected != op.Address() || procedure != nil && procedure.Address().ObjectID != op.Function {
			continue
		}
		spelling := [5]string{"<", "<=", "=", ">=", ">"}[strategy-1]
		if hasPredicate {
			operation, _ := comparisonOperation(spelling)
			collation := entry.Usage.Collation
			if pattern {
				collation = sqltype.CollationRef{ID: 950, Derivation: sqltype.Default}
			}
			expected, err := sqltype.BindComparisonPredicate(operation, left.ResultUsage().Type, right.ResultUsage().Type, node.ResultUsage().Type, collation, false)
			if err != nil || !expected.Equal(predicate) {
				return nil, nil, "", false
			}
		}
		return left, right, spelling, true
	}
	return nil, nil, "", false
}
