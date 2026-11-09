package stored

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// unreadViewLayout is authority for one immutable presentation in one types-only
// reownership operation. Keys are original nodes, never type IDs or source names.
// Standalone query remappers do not acquire this authority.
type unreadViewLayout map[plan.Node]map[int]sqltype.Usage

func analyzeUnreadViewLayout(current catalog.Read, owner *catalog.Relation) (unreadViewLayout, error) {
	definition := ViewOf(owner)
	if definition == nil || definition.temporary != (ids.SessionToken{}) || definition.query == nil {
		return nil, nil
	}
	var changes unreadViewLayout
	var failure error
	var seen map[int]*plan.SourceNode
	invalid := func() { failure = fmt.Errorf("stuntgres: invalid unread view source layout") }
	plan.VisitCTEQuery(definition.query.Root, func(node plan.Node) {
		if failure != nil {
			return
		}
		n, ok := node.(*plan.SourceNode)
		if !ok {
			return
		}
		if n.Occurrence < 0 || n.Occurrence >= len(definition.sources) {
			invalid()
			return
		}
		leaf := definition.sources[n.Occurrence]
		source, exists := current.Relation(leaf.Ref.Address)
		if !exists || leaf.Ref.Address.ClassID != ids.RelationClassID || leaf.Ref.Address.SubID != 0 || n.Ref != leaf.Ref || !ViewReferenceValid(current, owner, leaf.Ref) || len(n.Schema()) != len(leaf.Attributes) {
			invalid()
			return
		}
		// Inspect only differences of type identity. Same-ID generation changes
		// belong to the existing generic mapper, including its original policy.
		candidate := false
		if exists {
			for slot, attribute := range leaf.Attributes {
				if attribute <= 0 || slices.Contains(leaf.Attributes[:slot], attribute) || source.RelationKind == catalog.TableRelation && (n.Schema()[slot].OriginTable != uint32(leaf.Ref.Address.ObjectID) || int32(n.Schema()[slot].OriginAttribute) != attribute) {
					invalid()
					return
				}
				live := source.IndexForAttribute(attribute)
				if slot < len(n.Schema()) && live >= 0 && n.Schema()[slot].Usage.Type != nil && source.Columns[live].Usage.Type != nil && n.Schema()[slot].Usage.Type.ID() != source.Columns[live].Usage.Type.ID() {
					candidate = true
				}
			}
		}
		if !candidate {
			return
		}
		// Corroborate the occurrence in its original frozen frame, including
		// separate self aliases. This compares retained presentation metadata;
		// it never resolves a destination relation or attribute by name.
		associated := false
		for _, namespace := range definition.namespaces {
			if namespace.Node != analyze.ViewSourceNamespace || namespace.Ordinal != n.Occurrence || definition.namespaceRoot(namespace) != n {
				continue
			}
			for _, qualifier := range namespace.Qualifiers {
				associated = associated || qualifier.Name == n.RangeName
			}
		}
		if !associated {
			invalid()
			return
		}
		namespace, exists := current.Namespace(source.Namespace())
		if !exists || namespace.Temporary != (ids.SessionToken{}) {
			invalid()
			return
		}
		if seen == nil {
			seen = make(map[int]*plan.SourceNode)
		}
		if prior := seen[n.Occurrence]; prior != nil && prior != n {
			invalid()
			return
		}
		seen[n.Occurrence] = n
		for slot, attribute := range leaf.Attributes {
			live := source.IndexForAttribute(attribute)
			if attribute <= 0 || slices.Contains(leaf.Attributes[:slot], attribute) {
				invalid()
				return
			}
			if live < 0 {
				// An unrelated unread column may already have been dropped;
				// its frozen width is not a new attribute association.
				continue
			}
			old, target := n.Schema()[slot].Usage, source.Columns[live].Usage
			if old.Type == nil || target.Type == nil || old.Type.ID() == target.Type.ID() {
				continue
			}
			if slices.Contains(leaf.Reads, attribute) || leaf.WholeRowRead || slices.Contains(leaf.SystemColumns, attribute) {
				invalid()
				return
			}
			// This check must precede typ: a copied/foreign old descriptor must
			// never be laundered through its canonical replacement.
			if !ValidExpressionUsage(current, old) || !ValidExpressionUsage(current, target) {
				failure = fmt.Errorf("stuntgres: foreign view query type")
				return
			}
			if failure = ValidateUsageCollation(current, old); failure != nil {
				return
			}
			if failure = ValidateUsageCollation(current, target); failure != nil {
				return
			}
			if changes == nil {
				changes = make(unreadViewLayout)
			}
			if changes[n] == nil {
				changes[n] = make(map[int]sqltype.Usage)
			}
			changes[n][slot] = target
		}
	}, func(expr.Expr) {}, true)
	if failure != nil || len(changes) == 0 {
		return nil, failure
	}
	// Reads are the normal demand authority. Also refuse an executable column
	// capture of a changed attribute, rather than trusting a damaged Reads list.
	plan.VisitCTEQuery(definition.query.Root, func(plan.Node) {}, func(bound expr.Expr) {
		plan.WalkExpression(bound, func(bound expr.Expr) {
			column, ok := bound.(plan.ColumnExpr)
			if !ok {
				return
			}
			for node, slots := range changes {
				source, ok := node.(*plan.SourceNode)
				if !ok {
					continue
				}
				leaf := definition.sources[source.Occurrence]
				for slot := range slots {
					if column.Origin.Domain() == leaf.Ref.Domain() && column.Origin.Epoch == leaf.Ref.Epoch && column.Origin.Address.ClassID == leaf.Ref.Address.ClassID && column.Origin.Address.ObjectID == leaf.Ref.Address.ObjectID && column.Origin.Address.SubID == leaf.Attributes[slot] {
						invalid()
					}
				}
			}
		})
	}, true)
	if failure != nil {
		return nil, failure
	}
	// Transport only copied input positions. Project, DISTINCT, aggregate, set,
	// derived and CTE outputs are expression-owned boundaries, not source-layout copies.
	var transport func(plan.Node) error
	transport = func(node plan.Node) error {
		if node == nil {
			return nil
		}
		left, right := plan.QueryInputs(node)
		if err := transport(left); err != nil {
			return err
		}
		if err := transport(right); err != nil {
			return err
		}
		var nestedError error
		plan.QueryExpressions(node, func(bound expr.Expr) {
			plan.WalkExpression(bound, func(bound expr.Expr) {
				if nested, ok := bound.(plan.SubqueryExpr); ok && nested.QueryPlan != nil && nestedError == nil {
					nestedError = transport(nested.QueryPlan.Root)
				}
			})
		})
		if nestedError != nil {
			return nestedError
		}
		copyPrefix := func(child plan.Node, offset, width int) error {
			for slot, usage := range changes[child] {
				if slot >= width || offset+slot >= len(node.Schema()) || slot >= len(child.Schema()) || node.Schema()[offset+slot].Usage != child.Schema()[slot].Usage {
					return fmt.Errorf("stuntgres: invalid unread view copied usage")
				}
				if changes[node] == nil {
					changes[node] = make(map[int]sqltype.Usage)
				}
				changes[node][offset+slot] = usage
			}
			return nil
		}
		switch n := node.(type) {
		case *plan.FilterNode, *plan.SortNode, *plan.DistinctOnNode, *plan.LimitNode, *plan.RowLockNode, *plan.EvaluationNode, *plan.WindowNode:
			if left != nil {
				// Evaluation adds ORDER-cache values, not schema positions;
				// Window appends expression-owned slots after this input prefix.
				return copyPrefix(left, 0, len(left.Schema()))
			}
		case *plan.SetProjectNode:
			// Later set stages can already expose the full reserved schema.
			// Only BaseWidth positions belong to the original input frame;
			// marker/scalar positions remain owned by their expressions.
			return copyPrefix(n.Child, 0, n.BaseWidth)
		case *plan.DistinctNode:
			// Plain DISTINCT materializes Expressions into its own projected
			// schema; unlike DISTINCT ON, it does not retain the input slots.
			// Even a coincidentally equal-width/type output is not a copy.
			return nil
		case *plan.JoinNode:
			if n.LeftWidth != len(n.Left.Schema()) || n.RightWidth != len(n.Right.Schema()) {
				return fmt.Errorf("stuntgres: invalid unread view join layout")
			}
			if err := copyPrefix(n.Left, 0, n.LeftWidth); err != nil {
				return err
			}
			return copyPrefix(n.Right, n.LeftWidth, n.RightWidth)
		}
		return nil
	}
	if err := transport(definition.query.Root); err != nil {
		return nil, err
	}
	return changes, nil
}
