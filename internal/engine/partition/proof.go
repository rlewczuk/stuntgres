package partition

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This is a bounded implication check for partition predicates, not an expression
// evaluator or a second binder. CHECK permits UNKNOWN. Endpoint reasoning is
// therefore performed only in the non-NULL key domain; explicit RANGE must first
// prove that domain, whereas its complement already accepts every NULL key.
type ProofGoal struct {
	left, Right *ProofGoal
	key         int
	Op          string
	value       sqltype.Value
}

func proofAnd(a, b *ProofGoal) *ProofGoal {
	if a.Op == "false" || b.Op == "false" {
		return &ProofGoal{Op: "false"}
	}
	if a.Op == "true" {
		return b
	}
	if b.Op == "true" {
		return a
	}
	return &ProofGoal{Op: "and", left: a, Right: b}
}
func proofOr(a, b *ProofGoal) *ProofGoal {
	if a.Op == "true" || b.Op == "true" {
		return &ProofGoal{Op: "true"}
	}
	if a.Op == "false" {
		return b
	}
	if b.Op == "false" {
		return a
	}
	return &ProofGoal{Op: "or", left: a, Right: b}
}

// Tuple predicates are lexicographic. Infinite suffixes terminate the finite
// prefix; they are not ordinary type infinity values.
func ProofTuple(tuple []catalog.PartitionDatum, at int, greater bool) *ProofGoal {
	datum := tuple[at]
	if datum.Kind != 0 {
		truth := datum.Kind < 0
		if !greater {
			truth = !truth
		}
		if truth {
			return &ProofGoal{Op: "true"}
		}
		return &ProofGoal{Op: "false"}
	}
	op := "<"
	if greater {
		op = ">="
	}
	if at == len(tuple)-1 {
		return &ProofGoal{key: at, Op: op, value: datum.Value}
	}
	rest := ProofTuple(tuple, at+1, greater)
	strict := "<"
	if greater {
		strict = ">"
	}
	return proofOr(&ProofGoal{key: at, Op: strict, value: datum.Value}, proofAnd(&ProofGoal{key: at, Op: "=", value: datum.Value}, rest))
}

func ConstraintProven(ctx context.Context, input sqltype.InputEnvironment, view catalog.Read, parent, relation *catalog.Relation, bound *catalog.PartitionMembership, exclude bool) (bool, error) {
	// Range endpoint implication does not prove LIST or seeded HASH membership.
	// Private replay attachment uses ordinary full-row validation instead.
	if bound.Strategy != parser.PartitionRange {
		return false, ctx.Err()
	}
	var keys [32]expr.Expr
	in := analyze.LikeInput{Source: parent, TargetSlots: make([]int, len(parent.Columns))}
	for i, column := range parent.Columns {
		slot, ok := relation.Index[column.Name]
		if !ok {
			return false, ids.ErrCatalogReplaced
		}
		in.TargetSlots[i] = slot
	}
	for i, key := range parent.Partition.Keys {
		var node expr.Expr
		if key.Derived != nil {
			var err error
			node, err = stored.RemapLikeExpression(&in, key.Derived.Definition, relation, view)
			if err != nil {
				return false, err
			}
		} else {
			slot := in.TargetSlots[parent.IndexForAttribute(key.Attribute)]
			ref := view.Owner().Domain.Ref(view.Epoch(), relation.Address())
			ref.Address.SubID = relation.Attributes[slot]
			node = plan.ColumnExpr{Info: expr.Info{Usage: relation.Columns[slot].Usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: slot}
		}
		if node.ResultUsage().Collation.ID != key.Usage.Collation.ID {
			node = plan.CollationExpr{Info: expr.Info{Usage: key.Usage, Property: node.Properties()}, Child: node}
		}
		keys[i] = node
	}
	proof := prover{view: view, ctx: ctx, input: input, relation: relation, keys: keys[:len(parent.Partition.Keys)], entries: parent.Partition.Keys}
	goalFor := func(membership *catalog.PartitionMembership, complement bool) (*ProofGoal, bool) {
		if complement {
			return proofOr(ProofTuple(membership.Lower, 0, false), ProofTuple(membership.Upper, 0, true)), true
		}
		return proofAnd(ProofTuple(membership.Lower, 0, true), ProofTuple(membership.Upper, 0, false)), false
	}
	goal := &ProofGoal{Op: "true"}
	nullAllowed := exclude || bound.DefaultBound
	if bound.DefaultBound {
		for _, address := range parent.Partition.Children {
			child := view.MustRelation(address)
			if child.Membership.DefaultBound {
				continue
			}
			part, _ := goalFor(child.Membership, true)
			goal = proofAnd(goal, part)
		}
	} else {
		goal, _ = goalFor(bound, exclude)
	}
	if goal.Op == "true" && nullAllowed {
		return true, nil
	}
	if !nullAllowed {
		for _, key := range proof.keys {
			if !proof.nonNull(key) {
				return false, nil
			}
		}
	}
	return proof.implies(goal)
}

type prover struct {
	view     catalog.Read
	ctx      context.Context
	input    sqltype.InputEnvironment
	relation *catalog.Relation
	keys     []expr.Expr
	entries  []catalog.PartitionKeyEntry
}

func proofSame(a, b expr.Expr) bool {
	binder := analyze.Binder{Consumer: analyze.ConsumerContext{CheckDefinitionEquality: true}}
	return binder.SameGroupExpression(a, b)
}

func (p *prover) nonNull(key expr.Expr) bool {
	if column, ok := key.(plan.ColumnExpr); ok {
		for _, constraint := range p.relation.Constraints.NotNull {
			if constraint.Attribute == column.Origin.Address.SubID && !constraint.NotValid {
				return true
			}
		}
	}
	for _, check := range p.relation.Constraints.Check {
		if !check.NotValid && checkNonNull(check.Expression, key) {
			return true
		}
	}
	return false
}

func checkNonNull(node, key expr.Expr) bool {
	switch e := node.(type) {
	case plan.NullTestExpr:
		return e.Not && proofSame(e.Child, key)
	case plan.LogicalExpr:
		if e.Op == parser.LogicalAnd {
			return checkNonNull(e.Left, key) || checkNonNull(e.Right, key)
		}
		if e.Op == parser.LogicalOr {
			return checkNonNull(e.Left, key) && checkNonNull(e.Right, key)
		}
	}
	return false
}

func (p *prover) implies(goal *ProofGoal) (bool, error) {
	if err := p.ctx.Err(); err != nil {
		return false, err
	}
	if goal.Op == "true" {
		return true, nil
	}
	if goal.Op == "and" {
		a, err := p.implies(goal.left)
		if err != nil || !a {
			return a, err
		}
		return p.implies(goal.Right)
	}
	for _, check := range p.relation.Constraints.Check {
		if check.NotValid {
			continue
		}
		ok, err := p.fact(check.Expression, goal)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

func (p *prover) fact(node expr.Expr, goal *ProofGoal) (bool, error) {
	if goal.Op == "true" {
		return true, nil
	}
	if goal.Op == "and" {
		a, err := p.fact(node, goal.left)
		if err != nil || !a {
			return a, err
		}
		return p.fact(node, goal.Right)
	}
	// An OR fact needs every possible non-false branch. Different branches may
	// establish different alternatives of a lexicographic target predicate.
	if e, ok := node.(plan.LogicalExpr); ok && e.Op == parser.LogicalOr {
		a, err := p.fact(e.Left, goal)
		if err != nil || !a {
			return a, err
		}
		return p.fact(e.Right, goal)
	}
	if goal.Op == "or" {
		a, err := p.fact(node, goal.left)
		if err != nil || a {
			return a, err
		}
		a, err = p.fact(node, goal.Right)
		if err != nil || a {
			return a, err
		}
	}
	// A conjunction can supply facts jointly for an AND target, or a complete
	// disjunctive subpredicate for an OR target. Unknown unrelated facts cannot
	// establish a new comparison.
	if e, ok := node.(plan.LogicalExpr); ok && e.Op == parser.LogicalAnd {
		a, err := p.fact(e.Left, goal)
		if err != nil || a {
			return a, err
		}
		return p.fact(e.Right, goal)
	}
	if goal.Op == "or" {
		return false, nil
	}
	if e, ok := node.(plan.LiteralExpr); ok && e.Value.Type().ID() == sqltype.Bool {
		truth, err := plan.TruthOf(e.Value)
		return truth == plan.False, err
	}
	if e, ok := node.(plan.NullTestExpr); ok {
		for _, key := range p.keys {
			if proofSame(e.Child, key) {
				return !e.Not, nil
			}
		}
		return false, nil
	}
	e, ok := node.(plan.ComparisonExpr)
	if !ok || goal.Op == "false" {
		return false, nil
	}
	key := p.keys[goal.key]
	operator := e.Operator
	literal, matched := e.Right.(plan.LiteralExpr)
	if !proofSame(e.Left, key) || !matched {
		literal, matched = e.Left.(plan.LiteralExpr)
		if !matched || !proofSame(e.Right, key) {
			return false, nil
		}
		switch operator {
		case "<":
			operator = ">"
		case "<=":
			operator = ">="
		case ">":
			operator = "<"
		case ">=":
			operator = "<="
		}
	}
	entry := p.entries[goal.key]
	if literal.Value.IsNull() || literal.Value.Type() != entry.Usage.Type {
		return false, nil
	}
	// A custom index ordering is not automatically the CHECK operator's family.
	class, supported := sqltype.DefaultIndexClass(entry.Usage.Type)
	if !supported {
		return false, nil
	}
	ordering, err := routine.BindIndexOrdering(p.view, entry.Usage, class)
	if err != nil || !ordering.SameOperation(entry.Comparison) {
		return false, nil
	}
	collation, indeterminate := e.Predicate.InputCollation()
	if indeterminate || collation.ID != entry.Usage.Collation.ID {
		return false, nil
	}
	order := 0
	if !sqltype.SameDatum(literal.Value, goal.value) {
		order, err = entry.Comparison.Compare(p.ctx, literal.Value, goal.value, p.input)
		if err != nil {
			return false, err
		}
	}
	switch goal.Op {
	case ">=":
		return (operator == ">" || operator == ">=" || operator == "=") && order >= 0, nil
	case ">":
		return operator == ">" && order >= 0 || (operator == ">=" || operator == "=") && order > 0, nil
	case "<":
		return operator == "<" && order <= 0 || (operator == "<=" || operator == "=") && order < 0, nil
	case "<=":
		return (operator == "<" || operator == "<=" || operator == "=") && order <= 0, nil
	case "=":
		return operator == "=" && order == 0, nil
	}
	return false, nil
}
