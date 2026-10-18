package engine

import (
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
func partitionProofTuple(tuple []partitionDatum, at int, greater bool) *partitionProofGoal {
	datum := tuple[at]
	if datum.kind != 0 {
		truth := datum.kind < 0
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
		return &partitionProofGoal{key: at, op: op, value: datum.value}
	}
	rest := partitionProofTuple(tuple, at+1, greater)
	strict := "<"
	if greater {
		strict = ">"
	}
	return partitionProofOr(&partitionProofGoal{key: at, op: strict, value: datum.value}, partitionProofAnd(&partitionProofGoal{key: at, op: "=", value: datum.value}, rest))
}

func partitionConstraintProven(scope *StatementScope, catalog CatalogRead, parent, relation *relationDescriptor, bound *partitionMembership, exclude bool) (bool, error) {
	// Range endpoint implication does not prove seeded hash membership. Private
	// replay attachment uses the ordinary full-row validation path instead.
	if bound.strategy == parser.PartitionHash {
		return false, scope.op.Context().Err()
	}
	var keys [32]BoundExpr
	in := createLikeInput{source: parent, targetSlots: make([]int, len(parent.columns))}
	for i, column := range parent.columns {
		slot, ok := relation.index[column.Name]
		if !ok {
			return false, ErrCatalogReplaced
		}
		in.targetSlots[i] = slot
	}
	for i, key := range parent.partition.keys {
		var node BoundExpr
		if key.derived != nil {
			var err error
			node, err = in.remap(key.derived.definition, relation, catalog)
			if err != nil {
				return false, err
			}
		} else {
			slot := in.targetSlots[parent.indexForAttribute(key.attribute)]
			ref := catalog.owner.domain.Ref(catalog.Epoch(), relation.address)
			ref.Address.SubID = relation.attributes[slot]
			node = columnExpression{expressionInfo: expressionInfo{usage: relation.columns[slot].Usage, properties: ExprProperties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: slot}
		}
		if node.ResultUsage().Collation.ID != key.usage.Collation.ID {
			node = collationExpression{expressionInfo: expressionInfo{usage: key.usage, properties: node.Properties()}, child: node}
		}
		keys[i] = node
	}
	proof := partitionProof{scope: scope, relation: relation, keys: keys[:len(parent.partition.keys)], entries: parent.partition.keys}
	goalFor := func(membership *partitionMembership, complement bool) (*partitionProofGoal, bool) {
		if complement {
			return partitionProofOr(partitionProofTuple(membership.lower, 0, false), partitionProofTuple(membership.upper, 0, true)), true
		}
		return partitionProofAnd(partitionProofTuple(membership.lower, 0, true), partitionProofTuple(membership.upper, 0, false)), false
	}
	goal := &partitionProofGoal{op: "true"}
	nullAllowed := exclude || bound.defaultBound
	if bound.defaultBound {
		for _, address := range parent.partition.children {
			child := catalog.state.descriptors[address].(*relationDescriptor)
			if child.membership.defaultBound {
				continue
			}
			part, _ := goalFor(child.membership, true)
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
	relation *relationDescriptor
	keys     []BoundExpr
	entries  []partitionKeyEntry
}

func partitionProofSame(a, b BoundExpr) bool {
	binder := expressionBinder{checkDefinitionEquality: true}
	return binder.sameGroupExpression(a, b)
}

func (p *partitionProof) nonNull(key BoundExpr) bool {
	if column, ok := key.(columnExpression); ok {
		for _, constraint := range p.relation.notNullConstraints {
			if constraint.Attribute == column.origin.Address.SubID && !constraint.NotValid {
				return true
			}
		}
	}
	for _, check := range p.relation.checkConstraints {
		if !check.NotValid && partitionCheckNonNull(check.expression, key) {
			return true
		}
	}
	return false
}

func partitionCheckNonNull(node, key BoundExpr) bool {
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
	for _, check := range p.relation.checkConstraints {
		if check.NotValid {
			continue
		}
		ok, err := p.fact(check.expression, goal)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

func (p *partitionProof) fact(node BoundExpr, goal *partitionProofGoal) (bool, error) {
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
	if literal.value.IsNull() || literal.value.Type() != entry.usage.Type {
		return false, nil
	}
	// A custom index ordering is not automatically the CHECK operator's family.
	class, supported := sqltype.DefaultIndexClass(entry.usage.Type)
	if !supported {
		return false, nil
	}
	ordering, err := sqltype.BindIndexOrdering(entry.usage, class)
	if err != nil || ordering != entry.comparison {
		return false, nil
	}
	collation, indeterminate := e.predicate.InputCollation()
	if indeterminate || collation.ID != entry.usage.Collation.ID {
		return false, nil
	}
	order := 0
	if !sqltype.SameDatum(literal.value, goal.value) {
		order, err = entry.comparison.Compare(p.scope.op.Context(), literal.value, goal.value, p.scope.input)
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
