package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// These are only references still free at a FROM subtree boundary. A join's
// consumed local captures never escape into its enclosing join.
func fromCaptures(root plan.Node) []plan.FunctionCorrelation {
	switch n := root.(type) {
	case *plan.SourceNode:
		if n.Sample != nil {
			return n.Sample.Captures
		}
	case *plan.FunctionFromNode:
		return n.Captures
	case *plan.DerivedNode:
		return n.Captures
	case *plan.JoinNode:
		return n.Captures
	}
	return nil
}

func fromCaptureInNamespace(namespace *QueryNamespace, capture plan.FunctionCorrelation) bool {
	if namespace == nil {
		return false
	}
	if capture.QueryOccurrence != 0 {
		return queryNamespaceHasSystemAttribute(namespace, capture.QueryOccurrence)
	}
	for _, qualifier := range namespace.Qualifiers {
		if slot := queryPresenceSlot(qualifier.SourceRoot); slot != nil && *slot == capture.PresenceSlot {
			return true
		}
	}
	return false
}

func rebaseFromCaptures(captures []plan.FunctionCorrelation, frame uint32, prefix int) ([]plan.FunctionCorrelation, error) {
	var rebased []plan.FunctionCorrelation
	for i, capture := range captures {
		if capture.Frame != frame {
			continue
		}
		if capture.QueryOccurrence == 0 && capture.PresenceSlot == 0 && capture.Slot < prefix {
			return nil, fmt.Errorf("stuntgres: captured FROM prefix cannot be removed")
		}
		if rebased == nil {
			rebased = append([]plan.FunctionCorrelation(nil), captures...)
		}
		if capture.QueryOccurrence == 0 && capture.PresenceSlot == 0 {
			rebased[i].Slot -= prefix
		}
	}
	return rebased, nil
}

// RebaseFromCorrelation strips an unused ancestor prefix from invocation
// layouts, not ordinary output rows. Already-normalized descendants with no
// exported captures keep their local-left layout.
func RebaseFromCorrelation(host Host, view catalog.Read, root plan.Node, frame uint32, prefix int) (plan.Node, error) {
	rebased, err := rebaseFromCaptures(fromCaptures(root), frame, prefix)
	if err != nil || rebased == nil {
		return root, err
	}
	switch n := root.(type) {
	case *plan.SourceNode:
		rewrite, _ := fromCorrelationRewriter(host, view, frame, prefix)
		argument, _, err := rewrite(n.Sample.Argument)
		if err != nil {
			return nil, err
		}
		copy, sample := *n, *n.Sample
		sample.Argument, sample.Captures = argument, rebased
		copy.Sample = &sample
		return &copy, nil
	case *plan.JoinNode:
		left, err := RebaseFromCorrelation(host, view, n.Left, frame, prefix)
		if err != nil {
			return nil, err
		}
		right, correlationPrefix := n.Right, n.CorrelationPrefix
		// A zero prefix means this right operand has already been normalized
		// to local-left slots. Only the left may still export captures.
		if n.Frame == frame && correlationPrefix != 0 {
			if correlationPrefix < prefix {
				return nil, fmt.Errorf("stuntgres: invalid nested FROM correlation prefix")
			}
			right, err = RebaseFromCorrelation(host, view, right, frame, prefix)
			if err != nil {
				return nil, err
			}
			correlationPrefix -= prefix
		}
		copy := *n
		copy.Left, copy.Right, copy.Captures = left, right, rebased
		copy.CorrelationPrefix = correlationPrefix
		return &copy, nil
	case *plan.FunctionFromNode:
		rewrite, _ := fromCorrelationRewriter(host, view, frame, prefix)
		next, err := plan.RewriteQueryExpressions(n, rewrite)
		if err != nil {
			return nil, err
		}
		copy := *next.(*plan.FunctionFromNode)
		copy.Captures = rebased
		return &copy, nil
	case *plan.DerivedNode:
		_, query := fromCorrelationRewriter(host, view, frame, prefix)
		child, err := query(n.Query.Root)
		if err != nil {
			return nil, err
		}
		copy, owned := *n, *n.Query
		owned.Root = child
		copy.Query, copy.Captures = &owned, rebased
		return &copy, nil
	}
	return nil, fmt.Errorf("stuntgres: invalid FROM correlation owner")
}

// Child queries have distinct local frames. Only reads of the specified outer
// frame and their matching capture metadata move; child output slots and owner
// recipes do not. Sublinks use this same query mapper recursively.
func fromCorrelationRewriter(host Host, view catalog.Read, frame uint32, prefix int) (func(expr.Expr) (expr.Expr, bool, error), func(plan.Node) (plan.Node, error)) {
	var rewrite func(expr.Expr) (expr.Expr, bool, error)
	var query func(plan.Node) (plan.Node, error)
	rewrite = func(bound expr.Expr) (expr.Expr, bool, error) {
		used := false
		WalkQueryArgumentExpression(bound, func(node expr.Expr) {
			if column, ok := node.(plan.ColumnExpr); ok && column.Scope == 1 && column.Frame == frame {
				used = true
			}
		})
		if !used {
			return bound, false, nil
		}
		return host.MapBoundQueryExpression(view, bound, query, func(node expr.Expr) (expr.Expr, bool, error) {
			column, ok := node.(plan.ColumnExpr)
			if !ok {
				return node, false, nil
			}
			if column.Scope != 1 || column.Frame != frame {
				// A virtual owner may reuse this numeric frame privately. Never
				// descend its recipe when rebasing the lexical reference.
				return node, column.Virtual != nil, nil
			}
			if column.Slot < prefix || column.Virtual != nil && column.RowStart < prefix {
				return nil, false, fmt.Errorf("stuntgres: invalid rebased FROM argument slot")
			}
			column.Slot -= prefix
			if column.Virtual != nil {
				column.RowStart -= prefix
			}
			return column, true, nil
		})
	}
	query = func(root plan.Node) (plan.Node, error) {
		next, err := plan.RewriteQueryExpressions(root, rewrite, func(node plan.Node) (plan.Node, error) {
			if node == root {
				return node, nil
			}
			for _, capture := range fromCaptures(node) {
				if capture.Frame == frame {
					// This recursive call also owns the node's metadata. Returning
					// its copy prevents a second descent through the same subtree.
					return query(node)
				}
			}
			return node, nil
		})
		if err != nil {
			return nil, err
		}
		captures, err := rebaseFromCaptures(fromCaptures(next), frame, prefix)
		if err != nil || captures == nil {
			return next, err
		}
		switch n := next.(type) {
		case *plan.DerivedNode:
			copy := *n
			copy.Captures = captures
			return &copy, nil
		case *plan.FunctionFromNode:
			copy := *n
			copy.Captures = captures
			return &copy, nil
		case *plan.JoinNode:
			copy := *n
			copy.Captures = captures
			return &copy, nil
		case *plan.SourceNode:
			copy, sample := *n, *n.Sample
			sample.Captures = captures
			copy.Sample = &sample
			return &copy, nil
		}
		return nil, fmt.Errorf("stuntgres: invalid child FROM correlation owner")
	}
	return rewrite, query
}

// Prefix normalization copies FROM producers. Later whole-row binding must
// attach demanded presence to the accepted copy, not its discarded predecessor.
func rebindFromNamespace(namespace *QueryNamespace, before, after plan.Node) {
	if before == nil || before == after {
		return
	}
	for i := range namespace.Qualifiers {
		if namespace.Qualifiers[i].SourceRoot == before {
			namespace.Qualifiers[i].SourceRoot = after
		}
	}
	if _, child := before.(*plan.DerivedNode); child {
		return // Child-query namespace owners are outside this FROM frame.
	}
	leftBefore, rightBefore := plan.QueryInputs(before)
	leftAfter, rightAfter := plan.QueryInputs(after)
	rebindFromNamespace(namespace, leftBefore, leftAfter)
	rebindFromNamespace(namespace, rightBefore, rightAfter)
}

func (b *Binder) queryCaptures(roots []expr.Expr, calls bool) []plan.FunctionCorrelation {
	var captures []plan.FunctionCorrelation
	for _, root := range roots {
		arguments := []expr.Expr{root}
		if calls {
			arguments = root.(expr.Composite).Children()
		}
		for _, arg := range arguments {
			WalkQueryArgumentExpression(arg, func(node expr.Expr) {
				if capture, ok := b.fromCapture(node); ok {
					captures = append(captures, capture)
				}
			})
		}
	}
	return captures
}

func (b *Binder) derivedCaptures(root plan.Node) []plan.FunctionCorrelation {
	var captures []plan.FunctionCorrelation
	WalkQueryArgumentNode(root, func(node expr.Expr) {
		if capture, ok := b.fromCapture(node); ok {
			captures = append(captures, capture)
		}
	})
	return captures
}

func (b *Binder) fromCapture(node expr.Expr) (plan.FunctionCorrelation, bool) {
	if b.Query.Namespace == nil {
		return plan.FunctionCorrelation{}, false
	}
	if row, ok := node.(plan.RowExpr); ok && row.PresenceSlot != 0 && row.PresenceFrame == b.FrameID() {
		return plan.FunctionCorrelation{Frame: row.PresenceFrame, PresenceSlot: row.PresenceSlot, Name: row.WholeName, Pos: row.Pos}, true
	}
	if system, ok := node.(plan.SystemAttributeExpr); ok && system.Scope == 1 && system.Frame == b.FrameID() && system.QueryOccurrence > 0 {
		name := ""
		for _, qualifier := range b.Query.Namespace.Qualifiers {
			if qualifier.SystemOccurrence == system.QueryOccurrence {
				name = qualifier.Name
				break
			}
		}
		return plan.FunctionCorrelation{Frame: system.Frame, QueryOccurrence: system.QueryOccurrence, Name: name, Pos: system.Pos}, true
	}
	column, ok := node.(plan.ColumnExpr)
	if !ok || column.Scope != 1 || column.Frame != b.FrameID() {
		return plan.FunctionCorrelation{}, false
	}
	name := ""
	for _, qualifier := range b.Query.Namespace.Qualifiers {
		for _, candidate := range qualifier.Columns {
			if candidate.Slot == column.Slot {
				name = qualifier.Name
				break
			}
		}
		if name != "" {
			break
		}
	}
	return plan.FunctionCorrelation{Frame: column.Frame, Slot: column.Slot, Name: name, Pos: column.Pos}, true
}
