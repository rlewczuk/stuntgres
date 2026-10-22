package partition

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CapturedPruner borrows the same immutable topology and admitted prefixes as
// its source. Construction is metadata-only under admission; all proof work,
// comparison/hash providers and parameter reads happen in Select, unlocked.
type CapturedPruner struct {
	view   catalog.Read
	named  *catalog.Relation
	source *exec.Source
}

func CapturePruner(view catalog.Read, named *catalog.Relation, source *exec.Source) {
	if source == nil || source.RlsDenied {
		return
	}
	if captured, ok := view.Relation(source.Ref.Address); ok {
		named = captured
	}
	if source.Inheritance == nil && named.Membership == nil && len(named.Constraints.Check) == 0 && len(named.Constraints.NotNull) == 0 {
		return
	}
	source.BranchPruner = &CapturedPruner{view: view, named: named, source: source}
}

// mapped reuses the stored recipe mapper, replacing only captured physical
// columns that have a stable named-parent layout and a real lexical query Var.
// Extra physical columns, different occurrences/frames and opaque recipes are
// not proof facts. No expression is evaluated here.
func (p *pruneProof) mapped(node expr.Expr, physical *catalog.Relation) (expr.Expr, bool, error) {
	layout, layoutErr := catalog.InheritanceQueryLayout(p.named, physical)
	if layoutErr != nil {
		return node, false, nil
	}
	valid := true
	mapped, _, err := stored.RewriteViewExpression(p.view, node, physical, func(child expr.Expr) (expr.Expr, bool, error) {
		column, ok := child.(plan.ColumnExpr)
		if !ok {
			return child, false, nil
		}
		origin := column.Origin.Address
		origin.SubID = 0
		if column.Origin.Domain() != p.ref.Domain() || column.Origin.Epoch != p.ref.Epoch || origin != physical.Address() {
			valid = false
			return child, false, nil
		}
		slot := physical.IndexForAttribute(column.Origin.Address.SubID)
		if slot < 0 {
			valid = false
			return child, false, nil
		}
		namedSlot := -1
		if layout == nil && slot < len(p.named.Columns) {
			namedSlot = slot
		} else {
			for named, physicalSlot := range layout {
				if physicalSlot == slot {
					namedSlot = named
					break
				}
			}
		}
		if namedSlot < 0 {
			valid = false
			return child, false, nil
		}
		attribute := p.named.Attributes[namedSlot]
		var replacement expr.Expr
		for _, term := range p.terms {
			plan.WalkExpression(term.expression, func(candidate expr.Expr) {
				if c, ok := candidate.(plan.ColumnExpr); ok && c.Origin.Address.SubID == attribute && c.Slot == namedSlot {
					ref := c.Origin
					ref.Address.SubID = 0
					if ref == p.ref && c.Frame == p.evaluation.Frame && c.Scope == p.evaluation.Scope {
						replacement = c
					}
				}
			})
			if replacement != nil {
				return replacement, true, nil
			}
		}
		valid = false
		return child, false, nil
	})
	return mapped, valid, err
}

func (p *pruneProof) keyTerms(parent *catalog.Relation) ([32]int, error) {
	var result [32]int
	for i := range result {
		result[i] = -1
	}
	for i, key := range parent.Partition.Keys {
		class, ok := sqltype.DefaultIndexClass(key.Usage.Type)
		if !ok {
			continue
		}
		ordering, err := routine.BindIndexOrdering(p.view, key.Usage, class)
		if err != nil {
			continue
		}
		if parent.Partition.Strategy == parser.PartitionHash {
			hashClass, ok := sqltype.DefaultHashIndexClass(key.Usage.Type)
			if !ok || !hashClass.SameIdentity(key.Class) {
				continue
			}
			hash, err := routine.BindPartitionHash(p.view, key.Usage, hashClass)
			if err != nil || hash != key.Hash {
				continue
			}
		} else if !ordering.SameOperation(key.Comparison) {
			continue
		}
		var node expr.Expr
		if key.Derived != nil {
			node = key.Derived.Definition
		} else {
			slot := parent.IndexForAttribute(key.Attribute)
			if slot < 0 {
				continue
			}
			ref := p.ref.Domain().Ref(p.ref.Epoch, parent.Address())
			ref.Address.SubID = key.Attribute
			node = plan.ColumnExpr{Info: expr.Info{Usage: key.Usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Slot: slot}
		}
		mapped, ok, err := p.mapped(node, parent)
		if err != nil {
			return result, err
		}
		if !ok {
			continue
		}
		if mapped.ResultUsage().Collation.ID != key.Usage.Collation.ID {
			continue
		}
		term := p.term(mapped)
		if term >= 0 && p.terms[term].ordering.SameOperation(ordering) {
			result[i] = term
		}
	}
	return result, nil
}

func (p *pruneProof) tuple(goal *ProofGoal, terms [32]int) int {
	switch goal.Op {
	case "true":
		return 0
	case "false":
		return 1
	case "and", "or":
		return p.join(goal.Op, p.tuple(goal.left, terms), p.tuple(goal.Right, terms))
	default:
		return p.atom(terms[goal.key], goal.Op, goal.value)
	}
}
func (p *pruneProof) rangeMembership(parent *catalog.Relation, bound *catalog.PartitionMembership, terms [32]int) int {
	nonnull, nulls := 0, 1
	for i := range parent.Partition.Keys {
		nonnull = p.join("and", nonnull, p.atom(terms[i], "nonnull", sqltype.Value{}))
		nulls = p.join("or", nulls, p.atom(terms[i], "null", sqltype.Value{}))
	}
	if !bound.DefaultBound {
		return p.join("and", nonnull, p.join("and", p.tuple(ProofTuple(bound.Lower, 0, true), terms), p.tuple(ProofTuple(bound.Upper, 0, false), terms)))
	}
	result := 0
	for _, address := range parent.Partition.Children {
		child := p.view.MustRelation(address)
		if child.Membership.DefaultBound {
			continue
		}
		outside := p.join("or", p.tuple(ProofTuple(child.Membership.Lower, 0, false), terms), p.tuple(ProofTuple(child.Membership.Upper, 0, true), terms))
		result = p.join("and", result, p.join("or", nulls, outside))
	}
	return result
}

type pruneMembership struct {
	parent *catalog.Relation
	bound  *catalog.PartitionMembership
	terms  [32]int
}

func (p *pruneProof) constraints(physical *catalog.Relation) (int, error) {
	result := 0
	for _, check := range physical.Constraints.Check {
		if check.NotValid || exec.PlannedVolatility(check.Expression) != expr.Immutable || check.Expression.Properties().DomainDependent {
			continue
		}
		node, ok, err := p.mapped(check.Expression, physical)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		fact, err := p.normalize(node, true)
		if err != nil {
			return 0, err
		}
		result = p.join("and", result, fact)
	}
	for _, constraint := range physical.Constraints.NotNull {
		if constraint.NotValid {
			continue
		}
		slot := physical.IndexForAttribute(constraint.Attribute)
		if slot < 0 {
			continue
		}
		ref := p.ref.Domain().Ref(p.ref.Epoch, physical.Address())
		ref.Address.SubID = constraint.Attribute
		node := plan.ColumnExpr{Info: expr.Info{Usage: physical.Columns[slot].Usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Slot: slot}
		mapped, ok, err := p.mapped(node, physical)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		result = p.join("and", result, p.atom(p.term(mapped), "nonnull", sqltype.Value{}))
	}
	return result, nil
}

func (p *pruneProof) exact(domains []pruneDomain, term int) (sqltype.Value, bool, error) {
	if term < 0 {
		return sqltype.Value{}, false, nil
	}
	d := domains[term]
	if !d.point {
		return sqltype.Value{}, false, nil
	}
	if d.null && !d.nonnull {
		return sqltype.Null(p.terms[term].expression.ResultUsage().Type), true, nil
	}
	if d.null || !d.nonnull || !d.hasLower || !d.hasUpper || !d.lowerClosed || !d.upperClosed {
		return sqltype.Value{}, false, nil
	}
	order, err := p.compare(term, d.lower, d.upper)
	return d.lower, order == 0, err
}
func (p *pruneProof) finish(domains []pruneDomain, requirements []pruneMembership) (bool, error) {
	for _, requirement := range requirements {
		parent, bound := requirement.parent, requirement.bound
		var datums [32]catalog.PartitionDatum
		key := EvaluatedKey{Values: datums[:len(parent.Partition.Keys)]}
		complete := true
		for i := range parent.Partition.Keys {
			value, known, err := p.exact(domains, requirement.terms[i])
			if err != nil {
				return false, err
			}
			if !known {
				complete = false
				break
			}
			key.Values[i] = catalog.PartitionDatum{Value: value}
			key.anyNull = key.anyNull || value.IsNull()
		}
		if !complete {
			continue
		}
		if parent.Partition.Strategy == parser.PartitionHash {
			for i, entry := range parent.Partition.Keys {
				var err error
				key.Hash, err = AccumulateHash(p.ctx, entry, key.Values[i].Value, key.Hash)
				if err != nil {
					return false, err
				}
			}
		}
		if !bound.DefaultBound {
			contains, err := Contains(p.ctx, parent.Partition.Keys, bound, key, p.evaluation.Input)
			if err != nil || !contains {
				return false, err
			}
		} else {
			// LIST DEFAULT can be excluded by exact equality/NULL ownership,
			// never by inequalities or <> alone.
			for _, address := range parent.Partition.Children {
				child := p.view.MustRelation(address)
				if child.Membership.DefaultBound {
					continue
				}
				contains, err := Contains(p.ctx, parent.Partition.Keys, child.Membership, key, p.evaluation.Input)
				if err != nil || contains {
					return false, err
				}
			}
		}
	}
	return true, p.ctx.Err()
}

func (p *pruneProof) branch(query int, physical *catalog.Relation) (bool, error) {
	if err := p.ctx.Err(); err != nil {
		return false, err
	}
	if p.remaining == 0 {
		return true, nil
	}
	facts, err := p.constraints(physical)
	if err != nil {
		return false, err
	}
	root := p.join("and", query, facts)
	p.requirements = p.requirements[:0]
	for child := physical; child.Membership != nil; {
		parent, ok := p.view.Relation(child.Membership.Parent)
		if !ok {
			return false, ids.ErrCatalogReplaced
		}
		terms, err := p.keyTerms(parent)
		if err != nil {
			return false, err
		}
		bound := child.Membership
		switch parent.Partition.Strategy {
		case parser.PartitionRange:
			root = p.join("and", root, p.rangeMembership(parent, bound, terms))
		case parser.PartitionList:
			if bound.DefaultBound {
				p.requirements = append(p.requirements, pruneMembership{parent, bound, terms})
			} else {
				members := 1
				for _, datum := range bound.Values {
					value := datum.Value
					op := "="
					if value.IsNull() {
						op = "null"
					}
					members = p.join("or", members, p.atom(terms[0], op, value))
				}
				root = p.join("and", root, members)
			}
		case parser.PartitionHash:
			p.requirements = append(p.requirements, pruneMembership{parent, bound, terms})
		}
		child = parent
	}
	if cap(p.domains) < len(p.terms) {
		p.domains = make([]pruneDomain, len(p.terms))
	} else {
		p.domains = p.domains[:len(p.terms)]
	}
	for i := range p.domains {
		p.domains[i] = pruneDomain{null: true, nonnull: true}
	}
	return p.possible(&pruneTask{node: root}, p.domains, func(domains []pruneDomain) (bool, error) { return p.finish(domains, p.requirements) })
}

func (c *CapturedPruner) Select(ctx context.Context, evaluation *exec.Context, predicate expr.Expr) (exec.QueryBranchSelection, error) {
	if err := ctx.Err(); err != nil {
		return exec.QueryBranchSelection{}, err
	}
	proof := newPruneProof(ctx, evaluation, c.view, c.named, c.source.Ref)
	query, err := proof.normalize(predicate, false)
	if err != nil {
		return exec.QueryBranchSelection{}, err
	}
	if query == 0 {
		return exec.QueryBranchSelection{}, nil
	}
	queryNodes := len(proof.nodes)
	result := exec.QueryBranchSelection{}
	start := 0
	count := len(c.source.Inheritance)
	if c.source.Inheritance == nil {
		count = 1
	}
	for i := range count {
		physical, end := c.named, c.source.Count()
		if c.source.Inheritance != nil {
			physical = c.source.Inheritance[i].Leaf.Relation
			end = c.source.InheritanceEnds[i]
		}
		proof.nodes = proof.nodes[:queryNodes]
		possible, err := proof.branch(query, physical)
		if err != nil {
			return exec.QueryBranchSelection{}, err
		}
		if !possible && !result.Applied {
			result.Applied = true
			if start > 0 {
				result.Intervals = append(result.Intervals, exec.QueryBranchInterval{Start: 0, End: start})
			}
		}
		if possible && result.Applied && end > start {
			last := len(result.Intervals) - 1
			if last >= 0 && result.Intervals[last].End == start {
				result.Intervals[last].End = end
			} else {
				result.Intervals = append(result.Intervals, exec.QueryBranchInterval{Start: start, End: end})
			}
		}
		start = end
	}
	return result, ctx.Err()
}
