package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func comparisonOperator(name string) (sqltype.ComparisonOperator, bool) {
	switch name {
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

// Resolve the operator before finalizing unknown occurrences. The returned
// targets are the selected operation's actual inputs, not the source pair.
func (b *expressionBinder) resolveComparisonTypes(raw parser.OperatorExpr, lt, rt *sqltype.Descriptor) (*sqltype.Descriptor, *sqltype.Descriptor, error) {
	return b.resolveComparisonCandidates(raw, lt, rt, nil)
}

func (b *expressionBinder) resolveComparisonCandidates(raw parser.OperatorExpr, lt, rt *sqltype.Descriptor, candidates *operatorCandidateSet) (*sqltype.Descriptor, *sqltype.Descriptor, error) {
	// Internal provider dispatch has already selected and coerced these inputs.
	if len(raw.Args) == 2 && len(b.clause.selectedOperatorArgs) == 2 {
		left, lok := raw.Args[0].(parser.ParameterExpr)
		right, rok := raw.Args[1].(parser.ParameterExpr)
		if lok && rok && left.Number == -1 && right.Number == -2 {
			return lt, rt, nil
		}
	}
	operator, ok := comparisonOperator(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: invalid comparison operator")
	}
	var set operatorCandidateSet
	if candidates != nil {
		set = *candidates
	} else {
		var err error
		set, err = b.operatorCandidates(raw.Name, false)
		if err != nil {
			return nil, nil, err
		}
	}
	if set.hasCustom {
		selected, err := b.selectCatalogOperator(set, raw, lt, rt, false)
		if err != nil {
			return nil, nil, err
		}
		if err := requireBuiltinComparison(selected, raw.Pos); err != nil {
			return nil, nil, err
		}
		return b.comparisonInputType(sqltype.ID(selected.Left), lt, rt), b.comparisonInputType(sqltype.ID(selected.Right), rt, lt), nil
	}
	signature, err := sqltype.ResolveComparison(operator, lt, rt)
	if !set.available {
		err = sqlError("42883", "operator does not exist")
	}
	if err != nil {
		return nil, nil, functions.ComparisonError(b, err, lt, rt, raw)
	}
	return b.comparisonInputType(signature.Left, lt, rt), b.comparisonInputType(signature.Right, rt, lt), nil
}

// Specialize selected catalog formals without choosing another overload.
func (b *expressionBinder) comparisonInputType(id sqltype.ID, own, other *sqltype.Descriptor) *sqltype.Descriptor {
	polymorphic := !b.typ(id).Concrete() && id != 2249
	if own == nil && id == 2249 {
		return sqltype.DeferredRecord()
	}
	if id == 2249 && own != nil && own.BaseType().IsComposite() {
		return own.BaseType()
	}
	// Occurrence-owned RECORD shapes and polymorphic arrays retain the actual
	// provider's physical descriptors, not lookup-only catalog pseudo-types.
	for _, source := range [...]*sqltype.Descriptor{own, other} {
		if source != nil && (source.BaseType().ID() == id || polymorphic) {
			return source.BaseType()
		}
	}
	if id == 2249 {
		return sqltype.DeferredRecord()
	}
	return b.typ(id)
}

func (b *expressionBinder) bindArrayComparison(raw parser.ArrayComparisonExpr) (expr.Expr, error) {
	left, err := b.comparisonOperand(raw.Left)
	if err != nil {
		return nil, err
	}
	right, err := b.comparisonOperand(raw.Right)
	if err != nil {
		return nil, err
	}
	var element *sqltype.Descriptor
	if d := right.descriptor(); d != nil {
		base := d.BaseType()
		if !base.IsArray() {
			return nil, expr.ErrorAt("42809", "op ANY/ALL (array) requires array on right side", raw.Pos)
		}
		element = base.Element()
	}
	operatorRaw := parser.OperatorExpr{Name: raw.Name, Pos: raw.Pos}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operator, ok := comparisonOperator(name)
	set, err := b.operatorCandidates(raw.Name, false)
	if err != nil {
		return nil, err
	}
	var lt, rt *sqltype.Descriptor
	if set.hasCustom || !ok {
		selected, err := b.selectCatalogOperator(set, operatorRaw, left.descriptor(), element, false)
		if err != nil {
			return nil, err
		}
		if selected.Result != ids.OID(sqltype.Bool) {
			return nil, expr.ErrorAt("42809", "op ANY/ALL (array) requires operator to yield boolean", raw.Pos)
		}
		if err := requireBuiltinComparison(selected, raw.Pos); err != nil {
			return nil, err
		}
		lt = b.comparisonInputType(sqltype.ID(selected.Left), left.descriptor(), element)
		rt = b.comparisonInputType(sqltype.ID(selected.Right), element, left.descriptor())
		if !ok {
			return nil, functions.ComparisonError(b, sqlError("42883", "operator does not exist"), left.descriptor(), element, operatorRaw)
		}
	} else {
		signature, resolveErr := sqltype.ResolveComparison(operator, left.descriptor(), element)
		if !set.available {
			resolveErr = sqlError("42883", "operator does not exist")
		}
		if resolveErr != nil {
			return nil, functions.ComparisonError(b, resolveErr, left.descriptor(), element, operatorRaw)
		}
		lt = b.comparisonInputType(signature.Left, left.descriptor(), element)
		rt = b.comparisonInputType(signature.Right, element, left.descriptor())
	}
	declaration, _, err := b.comparisonRoutineDeclaration(name, lt, rt)
	if err != nil {
		return nil, err
	}
	l, err := b.coerceConditional(left, lt.Usage(), name)
	if err != nil {
		return nil, err
	}
	var target sqltype.Usage
	if right.node != nil && element != nil && element.BaseType() == rt {
		// The existing RHS already carries the resolved physical element.
		// Retain its exact domain/array generations, typmod and collation;
		// an array-valued element need not have an array-of-array provider.
		target = right.node.ResultUsage()
	} else {
		arrayType, found := b.definitionArray(rt)
		if !found || !arrayType.Concrete() {
			return nil, expr.ErrorAt("0A000", "unsupported array element type", raw.Pos)
		}
		target = arrayType.Usage()
	}
	r, err := b.coerceConditional(right, target, name)
	if err != nil {
		return nil, err
	}
	input, err := b.comparisonInfo(l, r)
	if err != nil {
		return nil, err
	}
	predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, r.ResultUsage().Type.BaseType().Element(), b.typ(sqltype.Bool), input.Usage.Collation, input.CollationConflict)
	if err != nil {
		return nil, functions.ComparisonError(b, err, left.descriptor(), element, operatorRaw)
	}
	properties := expr.MergeProperties(l.Properties(), r.Properties())
	if predicate.SessionDependent() && properties.Volatility < expr.Stable {
		properties.Volatility = expr.Stable
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	info := b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties)
	if declaration != nil {
		info, err = b.selectedBuiltinRoutineInfo(info, declaration)
		if err != nil {
			return nil, err
		}
	}
	return scalarArrayComparisonExpression{Info: info, left: l, array: r, predicate: predicate, operator: name, namespace: namespace, all: raw.All}, nil
}

func (b *expressionBinder) inListHasVar(node expr.Expr) bool {
	found := false
	frame := b.frameID()
	var visit func(expr.Expr)
	visit = func(child expr.Expr) {
		switch n := child.(type) {
		case columnExpression:
			found = found || n.frame == frame
		case returningWholeRowExpression:
			found = found || n.frame == frame
		case aggregateExpression:
			if n.frame == frame {
				if call, ok := b.aggregateCallAt(n); ok {
					call.expressions(func(expr expr.Expr) { walkQueryArgumentExpression(expr, visit) })
				}
			}
		}
	}
	walkQueryArgumentExpression(node, visit)
	return found
}

type storedExpressionSyntaxKey struct {
	frame    uint32
	position parser.Position
}

func (b *expressionBinder) bindInList(raw parser.InListExpr) (expr.Expr, error) {
	if len(raw.Items) == 0 {
		return nil, fmt.Errorf("stuntgres: empty IN list")
	}
	leftInput, err := b.inComparisonOperand(raw.Left)
	if err != nil {
		return nil, err
	}
	items := make([]comparisonOperand, len(raw.Items))
	var rowItems []inComparisonOperand
	if leftInput.constructor {
		rowItems = make([]inComparisonOperand, len(raw.Items))
	}
	nonvar := make([]comparisonOperand, 1, len(raw.Items)+1)
	vars := make([]bool, len(raw.Items))
	left := leftInput.scalar
	for i, child := range raw.Items {
		if leftInput.constructor {
			rowItems[i], err = b.inComparisonOperand(child)
			if err != nil {
				return nil, err
			}
			items[i] = rowItems[i].scalar
		} else {
			items[i], err = b.comparisonOperand(child)
			if err != nil {
				return nil, err
			}
		}
		vars[i] = b.inListHasVar(items[i].node)
		if leftInput.constructor && rowItems[i].constructor && items[i].node == nil {
			for _, field := range rowItems[i].fields {
				vars[i] = vars[i] || b.inListHasVar(field.node)
			}
		}
		if !vars[i] {
			if leftInput.constructor {
				nonvar = append(nonvar, b.inCommonOperand(rowItems[i]))
			} else {
				nonvar = append(nonvar, items[i])
			}
		}
	}
	nonvar[0] = b.inCommonOperand(leftInput)
	name, logical := "=", parser.LogicalOr
	if raw.Negate {
		name, logical = "<>", parser.LogicalAnd
	}
	operator, _ := comparisonOperator(name)
	op := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: raw.Pos}}}, Pos: raw.Pos}
	var result expr.Expr
	capture := b.params.noParameters || b.consumer.materializedDefinition
	var syntax parser.Expr
	batched := false
	if len(nonvar) > 2 {
		common, err := commonExpressionUsage(nonvar, b.typ(sqltype.Text).Usage(), "")
		if err != nil {
			return nil, err
		}
		feasible := common.Type != nil && common.Type.ID() != 2249
		if feasible {
			_, feasible = b.definitionArray(common.Type)
		}
		if feasible {
			for _, item := range nonvar {
				if item.descriptor() == nil {
					continue
				}
				if _, err := sqltype.PlanCoercion(item.node.ResultUsage(), common, sqltype.Implicit); err != nil {
					feasible = false
					break
				}
			}
		}
		if feasible {
			children := make([]expr.Expr, len(nonvar)-1)
			properties := expr.Properties{}
			for i, item := range nonvar[1:] {
				children[i], err = b.coerceConditional(item, common, "IN")
				if err != nil {
					return nil, err
				}
			}
			// The batch array inputs are finalized before scalar operator lookup
			// and lhs input, matching transformAExprIn's diagnostic precedence.
			leftTarget, rightTarget, err := b.resolveComparisonTypes(op, nonvar[0].descriptor(), common.Type)
			if err != nil {
				return nil, err
			}
			declaration, _, err := b.comparisonRoutineDeclaration(name, leftTarget, rightTarget)
			if err != nil {
				return nil, err
			}
			for i, child := range children {
				children[i], err = b.coerceConditional(comparisonOperand{node: child}, rightTarget.Usage(), name)
				if err != nil {
					return nil, err
				}
				properties = expr.MergeProperties(properties, children[i].Properties())
			}
			l, err := b.coerceConditional(left, leftTarget.Usage(), name)
			if err != nil {
				return nil, err
			}
			properties = expr.MergeProperties(properties, l.Properties())
			input, err := b.collatedInfo(b.info(rightTarget.Usage(), raw.Pos, properties), children...)
			if err != nil {
				return nil, err
			}
			input, err = b.collatedInfo(input, l)
			if err != nil {
				return nil, err
			}
			predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, children[0].ResultUsage().Type, b.typ(sqltype.Bool), input.Usage.Collation, input.CollationConflict)
			if err != nil {
				return nil, functions.ComparisonError(b, err, left.descriptor(), common.Type, op)
			}
			if predicate.SessionDependent() && properties.Volatility < expr.Stable {
				properties.Volatility = expr.Stable
			}
			info := b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties)
			if declaration != nil {
				info, err = b.selectedBuiltinRoutineInfo(info, declaration)
				if err != nil {
					return nil, err
				}
			}
			result = scalarArrayComparisonExpression{Info: info, left: l, items: children, predicate: predicate, operator: name, all: raw.Negate}
			// PostgreSQL coerces an unknown Param occurrence in place, unlike
			// an unknown Const. Only the original batch lhs is updated before
			// Var pairs copy it; other transformed occurrences stay independent.
			if parameter, ok := left.node.(parameterExpression); ok && parameter.Usage.Type == nil {
				left.node = l
			}
			batched = true
			if capture {
				elements := make([]parser.Expr, 0, len(children))
				for i, item := range raw.Items {
					if !vars[i] {
						elements = append(elements, item)
					}
				}
				syntax = parser.ArrayComparisonExpr{Left: raw.Left, Right: parser.ArrayExpr{Elements: elements, Pos: raw.Pos}, Name: op.Name, All: raw.Negate, Pos: raw.Pos}
			}
		}
	}
	for i, item := range items {
		if batched && !vars[i] {
			continue
		}
		var pair expr.Expr
		rowPair := leftInput.constructor && rowItems[i].constructor
		if rowPair {
			pair, err = b.bindRowPredicateOperands(op, operator, leftInput.fields, rowItems[i].fields, leftInput.expansions, rowItems[i].expansions, name)
		} else {
			if left.node == nil && leftInput.constructor {
				left, err = b.inScalarOperand(&leftInput)
				if err != nil {
					return nil, err
				}
			}
			if leftInput.constructor {
				item, err = b.inScalarOperand(&rowItems[i])
				if err != nil {
					return nil, err
				}
			}
			pair, err = b.bindComparisonOperands(op, operator, left, item)
		}
		if err != nil {
			return nil, err
		}
		var pairSyntax parser.Expr
		if capture {
			// Normalize only this residual pair. A mixed IN list can also
			// retain the same constructor's whole identity in its batch.
			args := []parser.Expr{raw.Left, raw.Items[i]}
			if rowPair {
				comparison := pair.(rowComparisonExpression)
				for side, input := range [2]inComparisonOperand{leftInput, rowItems[i]} {
					_, rawRow := input.scalar.raw.(parser.RowExpr)
					if constructor, ok := input.scalar.node.(rowExpression); ok && !rawRow {
						args[side] = *constructor.raw
						comparison.memberCasts[side] = true
					}
				}
				pair = comparison
			}
			pairSyntax = parser.OperatorExpr{Name: op.Name, Args: args, Pos: raw.Pos}
		}
		if result == nil {
			result = pair
			if capture {
				syntax = pairSyntax
			}
			continue
		}
		properties := expr.MergeProperties(result.Properties(), pair.Properties())
		result = logicalExpression{Info: b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties), op: logical, left: result, right: pair}
		if capture {
			syntax = parser.LogicalExpr{Op: logical, Left: syntax, Right: pairSyntax, Pos: raw.Pos}
		}
	}
	if capture {
		if b.stored.syntax == nil {
			b.stored.syntax = make(map[storedExpressionSyntaxKey]parser.Expr)
		}
		b.stored.syntax[storedExpressionSyntaxKey{frame: b.frameID(), position: raw.Pos}] = syntax
	}
	return result, nil
}

// Copy only changed list storage; the analyzed tree remains immutable.
func mapScalarArrayChildren(n scalarArrayComparisonExpression, child func(expr.Expr) (expr.Expr, bool, error)) (scalarArrayComparisonExpression, bool, error) {
	left, changed, err := child(n.left)
	if err != nil {
		return n, false, err
	}
	n.left = left
	if n.array != nil {
		array, replaced, err := child(n.array)
		if err != nil {
			return n, false, err
		}
		n.array = array
		changed = changed || replaced
	}
	var items []expr.Expr
	for i, old := range n.items {
		next, replaced, err := child(old)
		if err != nil {
			return n, false, err
		}
		if replaced && items == nil {
			items = append([]expr.Expr(nil), n.items...)
		}
		if items != nil {
			items[i] = next
		}
		changed = changed || replaced
	}
	if items != nil {
		n.items = items
	}
	return n, changed, nil
}

func renderScalarArrayComparison(n scalarArrayComparisonExpression, child func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	left, err := child(n.left)
	if err != nil {
		return "", err
	}
	var right string
	if n.array != nil {
		right, err = child(n.array)
		if err != nil {
			return "", err
		}
	} else {
		items := make([]string, len(n.items))
		for i, item := range n.items {
			items[i], err = child(item)
			if err != nil {
				return "", err
			}
		}
		right = "ARRAY[" + strings.Join(items, ", ") + "]"
	}
	operator := n.operator
	if n.namespace != "" {
		operator = "OPERATOR(" + identifier(n.namespace) + "." + operator + ")"
	}
	quantifier := "ANY"
	if n.all {
		quantifier = "ALL"
	}
	return "(" + left + " " + operator + " " + quantifier + " (" + right + "))", nil
}

func arithmeticOperator(name string) sqltype.ArithmeticOperator {
	switch name {
	case "-":
		return sqltype.ArithmeticSubtract
	case "*":
		return sqltype.ArithmeticMultiply
	case "/":
		return sqltype.ArithmeticDivide
	case "%":
		return sqltype.ArithmeticRemainder
	case "^":
		return sqltype.ArithmeticPower
	default:
		return sqltype.ArithmeticAdd
	}
}
