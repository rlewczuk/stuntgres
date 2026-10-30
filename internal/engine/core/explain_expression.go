package core

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func (r *explainRenderer) expression(root plan.Node, expression expr.Expr) (string, error) {
	deparser := stored.Deparser{View: r.auth.Catalog, Pretty: true, Output: r.scope.Output}
	if !r.options.verbose {
		deparser.Path = r.path
	}
	deparser.Output.Regclass = regobj.ClassOutput{View: r.auth.Catalog, Path: deparser.Path}
	deparser.ColumnSQL = func(column plan.ColumnExpr) (string, error) {
		if r.image != "" && column.Frame <= 1 {
			address := column.Origin.Address
			address.SubID = 0
			if address == r.policyTarget {
				if relation, ok := r.auth.Catalog.Relation(address); ok {
					name, err := storedAttribute(relation, column.Origin.Address.SubID)
					return r.image + "." + name, err
				}
			}
		}
		owner := root
		if frame := r.frames[column.Frame]; frame != nil && !explainContains(frame, root) {
			owner = frame
		}
		if owner != nil {
			if text, ok, err := r.slot(owner, column.Slot); ok || err != nil {
				return text, err
			}
		}
		address := column.Origin.Address
		address.SubID = 0
		if relation, ok := r.auth.Catalog.Relation(address); ok {
			name, err := deparser.Attribute(relation, column.Origin.Address.SubID)
			if err != nil {
				return "", err
			}
			return catalog.Identifier(relation.Name()) + "." + name, nil
		}
		return "", fmt.Errorf("stuntgres: EXPLAIN column has no bound presentation")
	}
	deparser.NodeSQL = func(node expr.Expr) (string, bool, error) {
		var text string
		var err error
		switch n := node.(type) {
		case plan.SubqueryExpr:
			if n.CTEEdge {
				return "", true, fmt.Errorf("stuntgres: analysis-only subquery in EXPLAIN output")
			}
			if n.QueryPlan == nil {
				return "", true, fmt.Errorf("stuntgres: missing EXPLAIN subquery")
			}
			label := r.subplan(n.QueryPlan)
			var operands []string
			for _, comparison := range n.Comparisons {
				part, failure := r.expression(root, comparison.Left)
				if failure != nil {
					return "", true, failure
				}
				operands = append(operands, part)
			}
			text = "(" + label + ")"
			if n.Kind == parser.SubqueryExists {
				text = "EXISTS (" + label + ")"
			} else if n.JSONArray != nil {
				text = "JSON_ARRAY(" + label + ")"
			} else if n.UpdateWidth != 0 {
				text = label + " output 1"
			}
			if len(operands) != 0 {
				quantifier := ""
				if n.Kind == parser.SubqueryAny {
					quantifier = " ANY"
				} else if n.Kind == parser.SubqueryAll {
					quantifier = " ALL"
				}
				text = "(" + strings.Join(operands, ", ") + " " + n.Operator + quantifier + " (" + label + "))"
			}
			if n.Negate {
				text = "NOT " + text
			}
		case plan.TableOIDExpr:
			text = "tableoid"
			if occurrence := r.occurrences[n.QueryOccurrence-1]; occurrence != nil {
				text = catalog.Identifier(occurrence.RangeName) + ".tableoid"
			}
		case plan.GroupExpr:
			text, err = r.groupExpression(root, n.Frame, n.Slot)
		case plan.AggregateExpr:
			owner := r.frameRoot(root, n.Frame)
			aggregate := explainAggregate(owner)
			if aggregate == nil || n.Slot < 0 || n.Slot >= len(aggregate.Calls) {
				err = fmt.Errorf("stuntgres: missing EXPLAIN aggregate owner")
			} else {
				text, err = r.aggregateCall(aggregate.Child, aggregate.Calls[n.Slot])
			}
		case plan.GroupingExpr:
			text, err = r.expressions(root, n.Args)
			text = "GROUPING(" + text + ")"
		case *plan.WindowExpr:
			window := explainWindow(r.frameRoot(root, n.Frame))
			if window == nil {
				err = fmt.Errorf("stuntgres: missing EXPLAIN window owner")
			} else {
				var ok bool
				text, ok, err = r.slot(window, n.Slot)
				if !ok && err == nil {
					err = fmt.Errorf("stuntgres: missing EXPLAIN window slot")
				}
			}
		case plan.CachedOrderExpr:
			text, err = r.cachedExpression(root, n.Slot)
		case plan.ReturningColumnExpr:
			image := r.image
			if n.Old {
				r.image = "old"
			} else if n.Absent || n.Merge {
				r.image = "new"
			}
			if n.Value != nil {
				text, err = r.expression(root, n.Value)
			} else {
				text, err = deparser.ColumnSQL(n.ColumnExpr)
			}
			r.image = image
		case plan.ReturningWholeRowExpr:
			image := r.image
			if n.Old {
				r.image = "old"
			} else {
				r.image = "new"
			}
			text, err = r.expressions(root, n.Members)
			text = "ROW(" + text + ")"
			r.image = image
		case plan.UpdateOutputExpr:
			if n.Group < 0 || n.Group >= len(r.assignmentRoots) {
				return "", true, fmt.Errorf("stuntgres: missing EXPLAIN assignment group")
			}
			var query *plan.Query
			plan.WalkDemandedExpression(r.assignmentRoots[n.Group], func(node expr.Expr) {
				if subquery, ok := node.(plan.SubqueryExpr); ok && subquery.UpdateWidth != 0 {
					query = subquery.QueryPlan
				}
			})
			if query == nil || n.Index < 0 || n.Index >= len(query.Root.Schema()) {
				return "", true, fmt.Errorf("stuntgres: missing EXPLAIN assignment output")
			}
			text = fmt.Sprintf("%s output %d", r.subplan(query), n.Index+1)
		case plan.ViewQualificationExpr:
			var conditions []string
			for i := len(n.Predicates) - 1; i >= 0; i-- {
				condition, failure := r.expression(root, n.Predicates[i])
				if failure != nil {
					return "", true, failure
				}
				conditions = append(conditions, "("+condition+")")
			}
			if n.User != nil {
				condition, failure := r.expression(root, n.User)
				if failure != nil {
					return "", true, failure
				}
				conditions = append(conditions, "("+condition+")")
			}
			if len(conditions) == 0 {
				return "", true, fmt.Errorf("stuntgres: missing EXPLAIN view qualification")
			}
			text = "(" + strings.Join(conditions, " AND ") + ")"
		default:
			return "", false, nil
		}
		return text, true, err
	}
	return deparser.SQL(r.scope.executionContext(), expression, stored.DeparseParent{})
}

func (r *explainRenderer) frameRoot(root plan.Node, frame uint32) plan.Node {
	if owner := r.frames[frame]; owner != nil {
		return owner
	}
	return root
}

func (r *explainRenderer) expressions(root plan.Node, expressions []expr.Expr) (string, error) {
	parts, err := r.expressionParts(root, expressions)
	return strings.Join(parts, ", "), err
}

func (r *explainRenderer) expressionParts(root plan.Node, expressions []expr.Expr) ([]string, error) {
	parts := make([]string, len(expressions))
	for i, expression := range expressions {
		text, err := r.expression(root, expression)
		if err != nil {
			return nil, err
		}
		parts[i] = text
	}
	return parts, nil
}

// Slot presentation follows the bound stage's actual output layout. It never
// uses runtime values or invents a literal for an intermediate slot.
func (r *explainRenderer) slot(root plan.Node, slot int) (string, bool, error) {
	if root == nil || slot < 0 {
		return "", false, nil
	}
	switch n := root.(type) {
	case *plan.SourceNode:
		return explainSchemaSlot(n.Schema(), slot, n.RangeName, n.ColumnAliases)
	case *plan.JoinNode:
		if slot < n.LeftWidth {
			return r.slot(n.Left, slot)
		}
		if slot < n.LeftWidth+n.RightWidth {
			return r.slot(n.Right, slot-n.LeftWidth)
		}
		index := slot - n.LeftWidth - n.RightWidth
		if index < len(n.Merges) {
			text, err := r.expression(n, n.Merges[index])
			return text, true, err
		}
	case *plan.ProjectNode:
		if n.RangeName != "" {
			return explainSchemaSlot(n.Schema(), slot, n.RangeName, n.ColumnAliases)
		}
		if slot < len(n.Expressions) {
			text, err := r.expression(n.Child, n.Expressions[slot])
			return text, true, err
		}
	case *plan.AggregateNode:
		text, err := r.aggregateSlot(n, slot)
		return text, true, err
	case *plan.WindowNode:
		width := len(n.Child.Schema())
		if slot < width {
			return r.slot(n.Child, slot)
		}
		for i, call := range n.Calls {
			if call.Expression != nil && call.Expression.Slot == slot {
				text, err := r.windowCall(n, i)
				return text, true, err
			}
		}
	case *plan.SetProjectNode:
		if slot < n.BaseWidth {
			return r.slot(n.Child, slot)
		}
		index := slot - n.BaseWidth
		for i, call := range n.Calls {
			position := i
			if n.CallSlots != nil {
				position = n.CallSlots[i]
			}
			if position == index {
				text, err := r.expression(n.Child, call)
				return text, true, err
			}
		}
		for i, scalar := range n.Scalars {
			position := i
			if n.ScalarSlots != nil {
				position = n.ScalarSlots[i]
			}
			if n.BaseWidth+n.MarkerWidth+position == slot {
				text, err := r.expression(n.Child, scalar)
				return text, true, err
			}
		}
		for _, scalar := range n.ScalarBindings {
			if column, ok := scalar.Column.(plan.ColumnExpr); ok && column.Slot == slot {
				text, err := r.expression(n.Child, scalar.Original)
				return text, true, err
			}
		}
		// Earlier SRF levels retain their produced slots in the shared frame.
		if slot < len(n.Child.Schema()) {
			return r.slot(n.Child, slot)
		}
	case *plan.DerivedNode:
		return explainSchemaSlot(n.Schema(), slot, explainDerivedAlias(n), n.ColumnAliases)
	case *plan.WorktableScanNode:
		return explainSchemaSlot(n.Schema(), slot, explainDerivedAlias(&n.DerivedNode), n.ColumnAliases)
	case *plan.FunctionFromNode:
		return explainSchemaSlot(n.Schema(), slot, n.RangeName, n.ColumnAliases)
	case *plan.ValuesNode, *plan.SetNode, *plan.RecursiveUnionNode:
		return explainSchemaSlot(root.Schema(), slot, "", nil)
	default:
		child, _ := plan.QueryInputs(root)
		return r.slot(child, slot)
	}
	return "", false, nil
}

// Private stage identities and reserved future SRF outputs have no SQL output
// at this stage. Follow their real layout through pass-through inputs rather
// than displaying an invented expression or treating them as aggregate calls.
func explainPrivateSlot(root plan.Node, slot int) bool {
	switch n := root.(type) {
	case *plan.AggregateNode:
		return slot == len(n.Keys)+len(n.Calls) && slot < len(n.Schema())
	case *plan.SetProjectNode:
		if slot >= n.BaseWidth {
			for i := range n.Calls {
				position := i
				if n.CallSlots != nil {
					position = n.CallSlots[i]
				}
				if slot == n.BaseWidth+position {
					return false
				}
			}
			for i := range n.Scalars {
				position := i
				if n.ScalarSlots != nil {
					position = n.ScalarSlots[i]
				}
				if slot == n.BaseWidth+n.MarkerWidth+position {
					return false
				}
			}
			if slot >= len(n.Child.Schema()) {
				return slot < len(n.Schema())
			}
		}
		return explainPrivateSlot(n.Child, slot)
	case *plan.JoinNode:
		if slot < n.LeftWidth {
			return explainPrivateSlot(n.Left, slot)
		}
		if slot < n.LeftWidth+n.RightWidth {
			return explainPrivateSlot(n.Right, slot-n.LeftWidth)
		}
	case *plan.ProjectNode, *plan.DerivedNode, *plan.WorktableScanNode, *plan.FunctionFromNode:
		return false
	default:
		if child, _ := plan.QueryInputs(root); child != nil && slot < len(child.Schema()) {
			return explainPrivateSlot(child, slot)
		}
	}
	return false
}

func explainSchemaSlot(schema []catalog.Column, slot int, alias string, aliases []string) (string, bool, error) {
	if slot >= len(schema) {
		return "", false, nil
	}
	name := catalog.Identifier(schema[slot].Name)
	if slot < len(aliases) {
		name = catalog.Identifier(aliases[slot])
	}
	if alias != "" {
		name = catalog.Identifier(alias) + "." + name
	}
	return name, true, nil
}

func explainAggregate(root plan.Node) *plan.AggregateNode {
	for root != nil {
		if node, ok := root.(*plan.AggregateNode); ok {
			return node
		}
		root, _ = plan.QueryInputs(root)
	}
	return nil
}

func explainWindow(root plan.Node) *plan.WindowNode {
	for root != nil {
		if node, ok := root.(*plan.WindowNode); ok {
			return node
		}
		root, _ = plan.QueryInputs(root)
	}
	return nil
}

func (r *explainRenderer) groupExpression(root plan.Node, frame uint32, slot int) (string, error) {
	aggregate := explainAggregate(r.frameRoot(root, frame))
	if aggregate == nil {
		return "", fmt.Errorf("stuntgres: missing EXPLAIN group owner")
	}
	return r.aggregateSlot(aggregate, slot)
}

func (r *explainRenderer) aggregateSlot(aggregate *plan.AggregateNode, slot int) (string, error) {
	if slot < len(aggregate.Keys) {
		return r.expression(aggregate.Child, aggregate.Keys[slot])
	}
	slot -= len(aggregate.Keys)
	if slot < 0 || slot >= len(aggregate.Calls) {
		return "", fmt.Errorf("stuntgres: invalid EXPLAIN group slot")
	}
	return r.aggregateCall(aggregate.Child, aggregate.Calls[slot])
}

func (r *explainRenderer) aggregateCall(root plan.Node, call plan.AggregateCall) (string, error) {
	if call.Procedure == nil {
		return "", fmt.Errorf("stuntgres: missing EXPLAIN aggregate declaration")
	}
	ordered := call.Operation.Hypothetical() || call.Operation == functions.AggregatePercentileCont || call.Operation == functions.AggregatePercentileDisc || call.Operation == functions.AggregateMode
	if call.Procedure.Aggregate != nil {
		kind := call.Procedure.Aggregate.Definition.Kind
		ordered = ordered || kind == "o" || kind == "h"
	}
	name := r.objectName(call.Procedure.Namespace(), call.Procedure.Name())
	var text string
	if ordered {
		direct, err := r.expressions(root, call.Direct)
		if err != nil {
			return "", err
		}
		order, err := r.sortKeys(root, call.Order)
		if err != nil {
			return "", err
		}
		text = name + "(" + direct + ") WITHIN GROUP (ORDER BY " + order + ")"
	} else {
		args, err := r.expressions(root, call.Arguments)
		if err != nil {
			return "", err
		}
		if len(call.Arguments) == 0 {
			args = "*"
		}
		if len(call.Distinct) != 0 {
			args = "DISTINCT " + args
		}
		if len(call.Order) != 0 {
			order, err := r.sortKeys(root, call.Order)
			if err != nil {
				return "", err
			}
			args += " ORDER BY " + order
		}
		text = name + "(" + args + ")"
	}
	if call.Filter != nil {
		filter, err := r.expression(root, call.Filter)
		if err != nil {
			return "", err
		}
		text += " FILTER (WHERE " + filter + ")"
	}
	return text, nil
}

func (r *explainRenderer) cachedExpression(root plan.Node, slot int) (string, error) {
	for root != nil {
		switch n := root.(type) {
		case *plan.EvaluationNode:
			if index := slot - n.Base; index >= 0 && index < len(n.Expressions) {
				return r.expression(n.Child, n.Expressions[index])
			}
		case *plan.SortNode:
			for _, key := range n.Keys {
				if key.Cache == slot {
					return r.expression(n.Child, key.Expression)
				}
			}
			if index := slot - (n.CacheWidth - len(n.Outputs)); index >= 0 && index < len(n.Outputs) {
				return r.expression(n.Child, n.Outputs[index])
			}
		}
		root, _ = plan.QueryInputs(root)
	}
	return "", fmt.Errorf("stuntgres: missing EXPLAIN evaluation slot %s", strconv.Itoa(slot))
}

func explainContains(root, candidate plan.Node) bool {
	if root == nil {
		return false
	}
	if root == candidate {
		return true
	}
	switch root.(type) {
	case *plan.DerivedNode, *plan.WorktableScanNode:
		return false
	}
	left, right := plan.QueryInputs(root)
	return explainContains(left, candidate) || explainContains(right, candidate)
}

// A nil INSERT/MERGE bound cell is the command binder's DEFAULT request,
// not a folded NULL or an expression to evaluate.
func (r *explainRenderer) valueRow(root plan.Node, row []expr.Expr) (string, error) {
	parts, err := r.valueParts(root, row)
	return strings.Join(parts, ", "), err
}

func (r *explainRenderer) valueParts(root plan.Node, row []expr.Expr) ([]string, error) {
	parts := make([]string, len(row))
	for i, expression := range row {
		if expression == nil {
			parts[i] = "DEFAULT"
			continue
		}
		text, err := r.expression(root, expression)
		if err != nil {
			return nil, err
		}
		parts[i] = text
	}
	return parts, nil
}
