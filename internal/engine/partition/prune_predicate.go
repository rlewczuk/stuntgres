package partition

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A proof is a small Boolean constraint graph, not an executable replacement
// predicate. Unknown atoms are possible. Its leaves use admitted typed ordering;
// no row values, rendered SQL or native equality decide membership.
type pruneAtom struct {
	term  int
	op    string
	value sqltype.Value
}
type pruneNode struct {
	op          string
	left, right int
	atom        pruneAtom
}
type pruneTerm struct {
	expression expr.Expr
	ordering   sqltype.IndexComparison
}
type pruneDomain struct {
	null, nonnull                                bool
	lower, upper                                 sqltype.Value
	hasLower, hasUpper, lowerClosed, upperClosed bool
	point                                        bool // A compatible equality/IS NULL, not inferred from inequalities.
}

// Refutation is optional: bound Boolean backtracking across the whole Select,
// rather than granting each physical branch a fresh exponential search.
const pruneSearchLimit = 4096

type pruneProof struct {
	ctx          context.Context
	evaluation   *exec.Context
	ref          ids.ObjectRef
	named        *catalog.Relation
	view         catalog.Read
	nodes        []pruneNode
	terms        []pruneTerm
	domains      []pruneDomain
	requirements []pruneMembership
	remaining    int
}

func newPruneProof(ctx context.Context, evaluation *exec.Context, view catalog.Read, named *catalog.Relation, ref ids.ObjectRef) pruneProof {
	return pruneProof{ctx: ctx, evaluation: evaluation, view: view, named: named, ref: ref, nodes: []pruneNode{{op: "true"}, {op: "false"}}, remaining: pruneSearchLimit}
}
func (p *pruneProof) join(op string, a, b int) int {
	if op == "and" {
		if a == 1 || b == 1 {
			return 1
		}
		if a == 0 {
			return b
		}
		if b == 0 {
			return a
		}
	} else {
		if a == 0 || b == 0 {
			return 0
		}
		if a == 1 {
			return b
		}
		if b == 1 {
			return a
		}
	}
	p.nodes = append(p.nodes, pruneNode{op: op, left: a, right: b})
	return len(p.nodes) - 1
}
func (p *pruneProof) atom(term int, op string, value sqltype.Value) int {
	if term < 0 {
		return 0
	}
	p.nodes = append(p.nodes, pruneNode{op: "atom", atom: pruneAtom{term: term, op: op, value: value}})
	return len(p.nodes) - 1
}
func (p *pruneProof) eligible(node expr.Expr) bool {
	properties := node.Properties()
	if properties.Volatility != expr.Immutable || properties.StageDependent || properties.ParameterDependent || properties.DomainDependent {
		return false
	}
	found, valid := false, true
	plan.WalkExpression(node, func(child expr.Expr) {
		if c, ok := child.(plan.ColumnExpr); ok {
			found = true
			ref := c.Origin
			ref.Address.SubID = 0
			slot := p.named.IndexForAttribute(c.Origin.Address.SubID)
			if ref != p.ref || c.Frame != p.evaluation.Frame || c.Scope != p.evaluation.Scope || slot < 0 || slot != c.Slot || c.Virtual != nil || c.MergeSource {
				valid = false
			}
		}
	})
	return found && valid
}
func (p *pruneProof) term(node expr.Expr) int {
	if !p.eligible(node) {
		return -1
	}
	for i, t := range p.terms {
		if proofSame(t.expression, node) {
			return i
		}
	}
	class, ok := p.view.UniqueDefaultIndexClass(403, node.ResultUsage().Type)
	if !ok {
		return -1
	}
	ordering, err := routine.BindIndexOrdering(p.view, node.ResultUsage(), class)
	if err != nil {
		return -1
	}
	p.terms = append(p.terms, pruneTerm{expression: node, ordering: ordering})
	return len(p.terms) - 1
}
func reversedPruneOperator(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	}
	return op
}
func pruneOperator(op string) (sqltype.ComparisonOperator, bool) {
	switch op {
	case "=":
		return sqltype.CompareEqual, true
	case "<>", "!=":
		return sqltype.CompareNotEqual, true
	case "<":
		return sqltype.CompareLess, true
	case "<=":
		return sqltype.CompareLessEqual, true
	case ">":
		return sqltype.CompareGreater, true
	case ">=":
		return sqltype.CompareGreaterEqual, true
	}
	return 0, false
}

// check=true recognizes the CHECK acceptance domain (TRUE or UNKNOWN), not
// WHERE's TRUE-only domain. No speculative evaluation of functions/coercions.
func (p *pruneProof) normalize(node expr.Expr, check bool) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	if logical, ok := node.(plan.LogicalExpr); ok && (logical.Op == parser.LogicalAnd || logical.Op == parser.LogicalOr) {
		a, err := p.normalize(logical.Left, check)
		if err != nil {
			return 0, err
		}
		b, err := p.normalize(logical.Right, check)
		if err != nil {
			return 0, err
		}
		op := "and"
		if logical.Op == parser.LogicalOr {
			op = "or"
		}
		return p.join(op, a, b), nil
	}
	if null, ok := node.(plan.NullTestExpr); ok {
		term := p.term(null.Child)
		op := "null"
		if null.Not {
			op = "nonnull"
		}
		return p.atom(term, op, sqltype.Value{}), nil
	}
	if comparison, ok := node.(plan.ComparisonExpr); ok {
		key, probe, operator := comparison.Left, comparison.Right, comparison.Operator
		value, known, err := plan.QueryPredicateProofValue(p.evaluation, probe)
		if err != nil {
			return 0, err
		}
		if !known {
			key, probe = comparison.Right, comparison.Left
			operator = reversedPruneOperator(operator)
			value, known, err = plan.QueryPredicateProofValue(p.evaluation, probe)
			if err != nil {
				return 0, err
			}
		}
		if !known {
			return 0, nil
		}
		term := p.term(key)
		if term < 0 || value.Type() != key.ResultUsage().Type {
			return 0, nil
		}
		op, ok := pruneOperator(comparison.Operator)
		if !ok {
			return 0, nil
		}
		usage := key.ResultUsage()
		canonical, err := sqltype.BindComparisonPredicate(op, usage.Type, usage.Type, comparison.ResultUsage().Type, usage.Collation, false)
		if err != nil || !comparison.Predicate.Equal(canonical) {
			return 0, nil
		}
		if value.IsNull() {
			if check {
				return 0, nil
			}
			return 1, nil
		}
		var result int
		if operator == "<>" || operator == "!=" {
			result = p.join("or", p.atom(term, "<", value), p.atom(term, ">", value))
		} else {
			result = p.atom(term, operator, value)
		}
		if check {
			result = p.join("or", p.atom(term, "null", sqltype.Value{}), result)
		}
		return result, nil
	}
	value, known, err := plan.QueryPredicateProofValue(p.evaluation, node)
	if err != nil {
		return 0, err
	}
	if known && value.Type() != nil && value.Type().ID() == sqltype.Bool {
		truth, err := plan.TruthOf(value)
		if err != nil {
			return 0, err
		}
		if truth == plan.True || check && truth != plan.False {
			return 0, nil
		}
		return 1, nil
	}
	return 0, nil
}

func (p *pruneProof) compare(term int, a, b sqltype.Value) (int, error) {
	return p.terms[term].ordering.Compare(p.ctx, a, b, p.evaluation.Input)
}
func (p *pruneProof) constrain(domains []pruneDomain, atom pruneAtom) (bool, error) {
	d := &domains[atom.term]
	switch atom.op {
	case "null":
		d.nonnull = false
		d.point = true
	case "nonnull":
		d.null = false
	default:
		d.null = false
		if atom.op == "=" {
			d.point = true
		}
		if atom.op == "=" || atom.op == ">" || atom.op == ">=" {
			closed := atom.op != ">"
			order := 1
			var err error
			if d.hasLower {
				order, err = p.compare(atom.term, atom.value, d.lower)
				if err != nil {
					return false, err
				}
			}
			if !d.hasLower || order > 0 {
				d.lower, d.hasLower, d.lowerClosed = atom.value, true, closed
			} else if order == 0 {
				d.lowerClosed = d.lowerClosed && closed
			}
		}
		if atom.op == "=" || atom.op == "<" || atom.op == "<=" {
			closed := atom.op != "<"
			order := -1
			var err error
			if d.hasUpper {
				order, err = p.compare(atom.term, atom.value, d.upper)
				if err != nil {
					return false, err
				}
			}
			if !d.hasUpper || order < 0 {
				d.upper, d.hasUpper, d.upperClosed = atom.value, true, closed
			} else if order == 0 {
				d.upperClosed = d.upperClosed && closed
			}
		}
	}
	if d.nonnull && d.hasLower && d.hasUpper {
		order, err := p.compare(atom.term, d.lower, d.upper)
		if err != nil {
			return false, err
		}
		if order > 0 || order == 0 && !(d.lowerClosed && d.upperClosed) {
			d.nonnull = false
		}
	}
	return d.null || d.nonnull, nil
}

type pruneTask struct {
	node int
	next *pruneTask
}

func (p *pruneProof) possible(task *pruneTask, domains []pruneDomain, finish func([]pruneDomain) (bool, error)) (bool, error) {
	if err := p.ctx.Err(); err != nil {
		return false, err
	}
	if p.remaining == 0 {
		return true, nil
	}
	p.remaining--
	if task == nil {
		return finish(domains)
	}
	node := p.nodes[task.node]
	switch node.op {
	case "true":
		return p.possible(task.next, domains, finish)
	case "false":
		return false, nil
	case "and":
		right := pruneTask{node: node.right, next: task.next}
		left := pruneTask{node: node.left, next: &right}
		return p.possible(&left, domains, finish)
	case "or":
		left := pruneTask{node: node.left, next: task.next}
		possible, err := p.possible(&left, domains, finish)
		if err != nil || possible {
			return possible, err
		}
		right := pruneTask{node: node.right, next: task.next}
		return p.possible(&right, domains, finish)
	default:
		old := domains[node.atom.term]
		possible, err := p.constrain(domains, node.atom)
		if err == nil && possible {
			possible, err = p.possible(task.next, domains, finish)
		}
		domains[node.atom.term] = old
		return possible, err
	}
}
