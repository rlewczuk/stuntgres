package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This is a bounded implication check for partition predicates, not an expression
// evaluator or a second binder. CHECK permits UNKNOWN. Endpoint reasoning is
// therefore performed only in the non-NULL key domain; explicit RANGE must first
// prove that domain, whereas its complement already accepts every NULL key.
type partitionProofGoal struct {
	left, right *partitionProofGoal
	key         int
	op          string
	value       sqltype.Value
}

func partitionProofAnd(a, b *partitionProofGoal) *partitionProofGoal {
	if a.op == "false" || b.op == "false" {
		return &partitionProofGoal{op: "false"}
	}
	if a.op == "true" {
		return b
	}
	if b.op == "true" {
		return a
	}
	return &partitionProofGoal{op: "and", left: a, right: b}
}
func partitionProofOr(a, b *partitionProofGoal) *partitionProofGoal {
	if a.op == "true" || b.op == "true" {
		return &partitionProofGoal{op: "true"}
	}
	if a.op == "false" {
		return b
	}
	if b.op == "false" {
		return a
	}
	return &partitionProofGoal{op: "or", left: a, right: b}
}

// Tuple predicates are lexicographic. Infinite suffixes terminate the finite
// prefix; they are not ordinary type infinity values.
func partitionProofTuple(tuple []catalog.PartitionDatum, at int, greater bool) *partitionProofGoal {
	datum := tuple[at]
	if datum.Kind != 0 {
		truth := datum.Kind < 0
		if !greater {
			truth = !truth
		}
		if truth {
			return &partitionProofGoal{op: "true"}
		}
		return &partitionProofGoal{op: "false"}
	}
	op := "<"
	if greater {
		op = ">="
	}
	if at == len(tuple)-1 {
		return &partitionProofGoal{key: at, op: op, value: datum.Value}
	}
	rest := partitionProofTuple(tuple, at+1, greater)
	strict := "<"
	if greater {
		strict = ">"
	}
	return partitionProofOr(&partitionProofGoal{key: at, op: strict, value: datum.Value}, partitionProofAnd(&partitionProofGoal{key: at, op: "=", value: datum.Value}, rest))
}

func partitionConstraintProven(scope *StatementScope, view catalog.Read, parent, relation *catalog.Relation, bound *catalog.PartitionMembership, exclude bool) (bool, error) {
	// Range endpoint implication does not prove seeded hash membership. Private
	// replay attachment uses the ordinary full-row validation path instead.
	if bound.Strategy == parser.PartitionHash {
		return false, scope.op.Context().Err()
	}
	var keys [32]expr.Expr
	in := createLikeInput{source: parent, targetSlots: make([]int, len(parent.Columns))}
	for i, column := range parent.Columns {
		slot, ok := relation.Index[column.Name]
		if !ok {
			return false, ids.ErrCatalogReplaced
		}
		in.targetSlots[i] = slot
	}
	for i, key := range parent.Partition.Keys {
		var node expr.Expr
		if key.Derived != nil {
			var err error
			node, err = in.remap(key.Derived.Definition, relation, view)
			if err != nil {
				return false, err
			}
		} else {
			slot := in.targetSlots[parent.IndexForAttribute(key.Attribute)]
			ref := view.Owner().Domain.Ref(view.Epoch(), relation.Address())
			ref.Address.SubID = relation.Attributes[slot]
			node = columnExpression{Info: expr.Info{Usage: relation.Columns[slot].Usage, Property: expr.Properties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: slot}
		}
		if node.ResultUsage().Collation.ID != key.Usage.Collation.ID {
			node = collationExpression{Info: expr.Info{Usage: key.Usage, Property: node.Properties()}, child: node}
		}
		keys[i] = node
	}
	proof := partitionProof{scope: scope, relation: relation, keys: keys[:len(parent.Partition.Keys)], entries: parent.Partition.Keys}
	goalFor := func(membership *catalog.PartitionMembership, complement bool) (*partitionProofGoal, bool) {
		if complement {
			return partitionProofOr(partitionProofTuple(membership.Lower, 0, false), partitionProofTuple(membership.Upper, 0, true)), true
		}
		return partitionProofAnd(partitionProofTuple(membership.Lower, 0, true), partitionProofTuple(membership.Upper, 0, false)), false
	}
	goal := &partitionProofGoal{op: "true"}
	nullAllowed := exclude || bound.DefaultBound
	if bound.DefaultBound {
		for _, address := range parent.Partition.Children {
			child := view.MustRelation(address)
			if child.Membership.DefaultBound {
				continue
			}
			part, _ := goalFor(child.Membership, true)
			goal = partitionProofAnd(goal, part)
		}
	} else {
		goal, _ = goalFor(bound, exclude)
	}
	if goal.op == "true" && nullAllowed {
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

type partitionProof struct {
	scope    *StatementScope
	relation *catalog.Relation
	keys     []expr.Expr
	entries  []catalog.PartitionKeyEntry
}

func partitionProofSame(a, b expr.Expr) bool {
	binder := expressionBinder{checkDefinitionEquality: true}
	return binder.sameGroupExpression(a, b)
}

func (p *partitionProof) nonNull(key expr.Expr) bool {
	if column, ok := key.(columnExpression); ok {
		for _, constraint := range p.relation.Constraints.NotNull {
			if constraint.Attribute == column.origin.Address.SubID && !constraint.NotValid {
				return true
			}
		}
	}
	for _, check := range p.relation.Constraints.Check {
		if !check.NotValid && partitionCheckNonNull(check.Expression, key) {
			return true
		}
	}
	return false
}

func partitionCheckNonNull(node, key expr.Expr) bool {
	switch e := node.(type) {
	case nullTestExpression:
		return e.not && partitionProofSame(e.child, key)
	case logicalExpression:
		if e.op == parser.LogicalAnd {
			return partitionCheckNonNull(e.left, key) || partitionCheckNonNull(e.right, key)
		}
		if e.op == parser.LogicalOr {
			return partitionCheckNonNull(e.left, key) && partitionCheckNonNull(e.right, key)
		}
	}
	return false
}

func (p *partitionProof) implies(goal *partitionProofGoal) (bool, error) {
	if err := p.scope.op.Context().Err(); err != nil {
		return false, err
	}
	if goal.op == "true" {
		return true, nil
	}
	if goal.op == "and" {
		a, err := p.implies(goal.left)
		if err != nil || !a {
			return a, err
		}
		return p.implies(goal.right)
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

func (p *partitionProof) fact(node expr.Expr, goal *partitionProofGoal) (bool, error) {
	if goal.op == "true" {
		return true, nil
	}
	if goal.op == "and" {
		a, err := p.fact(node, goal.left)
		if err != nil || !a {
			return a, err
		}
		return p.fact(node, goal.right)
	}
	// An OR fact needs every possible non-false branch. Different branches may
	// establish different alternatives of a lexicographic target predicate.
	if e, ok := node.(logicalExpression); ok && e.op == parser.LogicalOr {
		a, err := p.fact(e.left, goal)
		if err != nil || !a {
			return a, err
		}
		return p.fact(e.right, goal)
	}
	if goal.op == "or" {
		a, err := p.fact(node, goal.left)
		if err != nil || a {
			return a, err
		}
		a, err = p.fact(node, goal.right)
		if err != nil || a {
			return a, err
		}
	}
	// A conjunction can supply facts jointly for an AND target, or a complete
	// disjunctive subpredicate for an OR target. Unknown unrelated facts cannot
	// establish a new comparison.
	if e, ok := node.(logicalExpression); ok && e.op == parser.LogicalAnd {
		a, err := p.fact(e.left, goal)
		if err != nil || a {
			return a, err
		}
		return p.fact(e.right, goal)
	}
	if goal.op == "or" {
		return false, nil
	}
	if e, ok := node.(literalExpression); ok && e.value.Type().ID() == sqltype.Bool {
		truth, err := TruthOf(e.value)
		return truth == False, err
	}
	if e, ok := node.(nullTestExpression); ok {
		for _, key := range p.keys {
			if partitionProofSame(e.child, key) {
				return !e.not, nil
			}
		}
		return false, nil
	}
	e, ok := node.(comparisonExpression)
	if !ok || goal.op == "false" {
		return false, nil
	}
	key := p.keys[goal.key]
	operator := e.operator
	literal, matched := e.right.(literalExpression)
	if !partitionProofSame(e.left, key) || !matched {
		literal, matched = e.left.(literalExpression)
		if !matched || !partitionProofSame(e.right, key) {
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
	if literal.value.IsNull() || literal.value.Type() != entry.Usage.Type {
		return false, nil
	}
	// A custom index ordering is not automatically the CHECK operator's family.
	class, supported := sqltype.DefaultIndexClass(entry.Usage.Type)
	if !supported {
		return false, nil
	}
	ordering, err := sqltype.BindIndexOrdering(entry.Usage, class)
	if err != nil || ordering != entry.Comparison {
		return false, nil
	}
	collation, indeterminate := e.predicate.InputCollation()
	if indeterminate || collation.ID != entry.Usage.Collation.ID {
		return false, nil
	}
	order := 0
	if !sqltype.SameDatum(literal.value, goal.value) {
		order, err = entry.Comparison.Compare(p.scope.op.Context(), literal.value, goal.value, p.scope.input)
		if err != nil {
			return false, err
		}
	}
	switch goal.op {
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
