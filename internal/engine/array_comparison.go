package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Scalar lists scan their typed children directly; array inputs scan the owner's
// immutable flat payload. Neither route constructs an array per input row.
type scalarArrayComparisonExpression struct {
	expressionInfo
	left, array         BoundExpr
	items               []BoundExpr
	predicate           sqltype.ComparisonPredicate
	operator, namespace string
	all                 bool
}

func (n scalarArrayComparisonExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	left, err := n.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var array sqltype.Value
	count := len(n.items)
	if n.array != nil {
		array, err = n.array.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if array.IsNull() {
			return sqltype.Null(n.usage.Type), nil
		}
		count = array.ArrayLen()
	}
	unknown := false
	decisive := false
	for i := range count {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		var right sqltype.Value
		if n.array != nil {
			right = array.ArrayElement(i)
		} else {
			right, err = n.items[i].Eval(c)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		// A batch corresponds to ARRAY[...] input: every item is evaluated
		// even after reduction becomes decisive, without allocating the array.
		if decisive {
			continue
		}
		value, err := n.predicate.Evaluate(c.Context, left, right, c.Input)
		if err != nil {
			return sqltype.Value{}, err
		}
		truth, err := TruthOf(value)
		if err != nil {
			return sqltype.Value{}, err
		}
		if truth == Unknown {
			unknown = true
			continue
		}
		if (!n.all && truth == True) || (n.all && truth == False) {
			if n.array != nil {
				return sqltype.Boolean(n.usage.Type, !n.all), nil
			}
			decisive = true
		}
	}
	if decisive {
		return sqltype.Boolean(n.usage.Type, !n.all), nil
	}
	if unknown {
		return sqltype.Null(n.usage.Type), nil
	}
	return sqltype.Boolean(n.usage.Type, n.all), nil
}

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

// resolveComparisonTypes is analysis-only and shared by ordinary and quantified
// comparisons. Unknown occurrences are not finalized or mutated by this probe.
func (b *expressionBinder) resolveComparisonTypes(raw parser.OperatorExpr, lt, rt *sqltype.Descriptor) (*sqltype.Descriptor, *sqltype.Descriptor, error) {
	originalLeft, originalRight := lt, rt
	operator, ok := comparisonOperator(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: invalid comparison operator")
	}
	if lt != nil && lt.BaseType().IsComposite() || rt != nil && rt.BaseType().IsComposite() {
		return nil, nil, expressionError("0A000", "composite comparison is unsupported", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, nil, err
	}
	if !visible {
		return nil, nil, b.comparisonError(sqlError("42883", "operator does not exist"), lt, rt, raw)
	}
	if lt != nil && rt != nil && (lt.BaseType().IsEnum() || rt.BaseType().IsEnum()) {
		if err := sqltype.CheckComparisonOperator(operator, lt, rt); err != nil {
			return nil, nil, b.comparisonError(err, lt, rt, raw)
		}
	}
	if lt != nil {
		lt = lt.BaseType()
	}
	if rt != nil {
		rt = rt.BaseType()
	}
	if lt == nil {
		lt = rt
	}
	if rt == nil {
		rt = lt
	}
	if lt == nil {
		lt, rt = b.typ(sqltype.Text), b.typ(sqltype.Text)
	}
	wantLeft, wantRight := b.comparisonCoercionTypes(lt, rt)
	// ACL has an equality provider, not the total-order provider probed below.
	// Resolve unsupported scalar operators before converting contextual text.
	if wantLeft == wantRight && wantLeft.ContainsACLItem() {
		scalar := wantLeft.ID() == sqltype.ACLItem
		array := wantLeft.IsArray() && wantLeft.Element().BaseType().ID() == sqltype.ACLItem
		if scalar && raw.Name.Parts[len(raw.Name.Parts)-1].Text != "=" || !scalar && !array {
			return nil, nil, b.comparisonError(sqlError("42883", "operator does not exist"), originalLeft, originalRight, raw)
		}
		return lt, rt, nil
	}
	if err := sqltype.CheckComparisonOperator(operator, wantLeft, wantRight); err != nil {
		return nil, nil, b.comparisonError(err, originalLeft, originalRight, raw)
	}
	return lt, rt, nil
}

// Comparison's existing implicit cross-family edges apply equally to array
// elements. Numeric width comparisons otherwise retain their exact providers.
func (b *expressionBinder) comparisonCoercionTypes(lt, rt *sqltype.Descriptor) (*sqltype.Descriptor, *sqltype.Descriptor) {
	integer := func(t *sqltype.Descriptor) bool {
		return t.ID() == sqltype.Int2 || t.ID() == sqltype.Int4 || t.ID() == sqltype.Int8
	}
	switch {
	case lt.ID() == sqltype.Xid && rt.ID() == sqltype.Int2:
		rt = b.typ(sqltype.Int4)
	case lt.ID() == sqltype.Inet && rt.ID() == sqltype.Cidr,
		lt.ID() == sqltype.Oid && integer(rt),
		lt.ID() == sqltype.Timetz && rt.ID() == sqltype.Time:
		rt = lt
	case rt.ID() == sqltype.Inet && lt.ID() == sqltype.Cidr,
		rt.ID() == sqltype.Oid && integer(lt),
		rt.ID() == sqltype.Timetz && lt.ID() == sqltype.Time:
		lt = rt
	}
	return lt, rt
}

func (b *expressionBinder) bindArrayComparison(raw parser.ArrayComparisonExpr) (BoundExpr, error) {
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
			return nil, expressionError("42809", "op ANY/ALL (array) requires array on right side", raw.Pos)
		}
		element = base.Element()
	}
	operatorRaw := parser.OperatorExpr{Name: raw.Name, Pos: raw.Pos}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operator, ok := comparisonOperator(name)
	if !ok {
		// Resolve the existing operator namespace/signature first. Arithmetic
		// operators never yield BOOL and cannot be scalar-array predicates.
		visible, err := b.builtinVisible(raw.Name, operatorLookup)
		if err != nil {
			return nil, err
		}
		if !visible || !arithmeticName(name) {
			return nil, b.comparisonError(sqlError("42883", "operator does not exist"), left.descriptor(), element, operatorRaw)
		}
		lt, rt := left.descriptor(), element
		if lt != nil {
			lt = lt.BaseType()
		}
		if rt != nil {
			rt = rt.BaseType()
		}
		if _, err := sqltype.ResolveArithmetic(arithmeticOperator(name), lt, rt, false); err != nil {
			return nil, b.comparisonError(err, left.descriptor(), element, operatorRaw)
		}
		return nil, expressionError("42809", "op ANY/ALL (array) requires operator to yield boolean", raw.Pos)
	}
	lt, rt, err := b.resolveComparisonTypes(operatorRaw, left.descriptor(), element)
	if err != nil {
		return nil, err
	}
	lt, rt = b.comparisonCoercionTypes(lt, rt)
	l, err := b.coerceConditional(left, lt.Usage(), name)
	if err != nil {
		return nil, err
	}
	arrayType, found := (CatalogRead{owner: b.engine, state: b.engine.state}).LookupArray(rt)
	if !found || !arrayType.Concrete() {
		return nil, expressionError("0A000", "unsupported array element type", raw.Pos)
	}
	target := arrayType.Usage()
	if right.node != nil && element == rt {
		// Preserve the original array's element typmod/collation/domain input.
		target = right.node.ResultUsage()
	}
	r, err := b.coerceConditional(right, target, name)
	if err != nil {
		return nil, err
	}
	input, err := b.comparisonInfo(l, r)
	if err != nil {
		return nil, err
	}
	predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, r.ResultUsage().Type.BaseType().Element(), b.typ(sqltype.Bool), input.usage.Collation, input.collationConflict)
	if err != nil {
		return nil, b.comparisonError(err, left.descriptor(), element, operatorRaw)
	}
	properties := mergeExpressionProperties(l.Properties(), r.Properties())
	if predicate.SessionDependent() && properties.Volatility < Stable {
		properties.Volatility = Stable
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return scalarArrayComparisonExpression{expressionInfo: b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties), left: l, array: r, predicate: predicate, operator: name, namespace: namespace, all: raw.All}, nil
}

func (b *expressionBinder) inListHasVar(node BoundExpr) bool {
	found := false
	frame := b.frameID()
	var visit func(BoundExpr)
	visit = func(child BoundExpr) {
		switch n := child.(type) {
		case columnExpression:
			found = found || n.frame == frame
		case returningWholeRowExpression:
			found = found || n.frame == frame
		case aggregateExpression:
			if n.frame == frame {
				if call, ok := b.aggregateCallAt(n); ok {
					call.expressions(func(expr BoundExpr) { walkQueryArgumentExpression(expr, visit) })
				}
			}
		}
	}
	walkQueryArgumentExpression(node, visit)
	return found
}

type inListSyntaxKey struct {
	frame    uint32
	position parser.Position
}

func (b *expressionBinder) bindInList(raw parser.InListExpr) (BoundExpr, error) {
	if len(raw.Items) == 0 {
		return nil, fmt.Errorf("stuntgres: empty IN list")
	}
	left, err := b.comparisonOperand(raw.Left)
	if err != nil {
		return nil, err
	}
	items := make([]comparisonOperand, len(raw.Items))
	nonvar := make([]comparisonOperand, 1, len(raw.Items)+1)
	nonvar[0] = left
	vars := make([]bool, len(raw.Items))
	for i, child := range raw.Items {
		items[i], err = b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		vars[i] = b.inListHasVar(items[i].node)
		if !vars[i] {
			nonvar = append(nonvar, items[i])
		}
	}
	name, logical := "=", parser.LogicalOr
	if raw.Negate {
		name, logical = "<>", parser.LogicalAnd
	}
	operator, _ := comparisonOperator(name)
	op := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: raw.Pos}}}, Pos: raw.Pos}
	var result BoundExpr
	capture := b.noParameters || b.materializedDefinition
	var syntax parser.Expr
	batched := false
	if len(nonvar) > 2 {
		common, err := commonExpressionUsage(nonvar, b.typ(sqltype.Text).Usage(), "")
		if err != nil {
			return nil, err
		}
		feasible := common.Type != nil
		if feasible {
			_, feasible = (CatalogRead{owner: b.engine, state: b.engine.state}).LookupArray(common.Type)
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
			children := make([]BoundExpr, len(nonvar)-1)
			properties := ExprProperties{}
			for i, item := range nonvar[1:] {
				children[i], err = b.coerceConditional(item, common, "IN")
				if err != nil {
					return nil, err
				}
				properties = mergeExpressionProperties(properties, children[i].Properties())
			}
			// The batch array inputs are finalized before scalar operator lookup
			// and lhs input, matching transformAExprIn's diagnostic precedence.
			leftTarget, _, err := b.resolveComparisonTypes(op, left.descriptor(), common.Type)
			if err != nil {
				return nil, err
			}
			leftTarget, _ = b.comparisonCoercionTypes(leftTarget, common.Type.BaseType())
			l, err := b.coerceConditional(left, leftTarget.Usage(), name)
			if err != nil {
				return nil, err
			}
			properties = mergeExpressionProperties(properties, l.Properties())
			input, err := b.collatedInfo(b.info(common, raw.Pos, properties), children...)
			if err != nil {
				return nil, err
			}
			input, err = b.collatedInfo(input, l)
			if err != nil {
				return nil, err
			}
			predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, children[0].ResultUsage().Type, b.typ(sqltype.Bool), input.usage.Collation, input.collationConflict)
			if err != nil {
				return nil, b.comparisonError(err, left.descriptor(), common.Type, op)
			}
			if predicate.SessionDependent() && properties.Volatility < Stable {
				properties.Volatility = Stable
			}
			result = scalarArrayComparisonExpression{expressionInfo: b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties), left: l, items: children, predicate: predicate, operator: name, all: raw.Negate}
			// PostgreSQL coerces an unknown Param occurrence in place, unlike
			// an unknown Const. Only the original batch lhs is updated before
			// Var pairs copy it; other transformed occurrences stay independent.
			if parameter, ok := left.node.(parameterExpression); ok && parameter.usage.Type == nil {
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
		pair, err := b.bindComparisonOperands(op, operator, left, item)
		if err != nil {
			return nil, err
		}
		if result == nil {
			result = pair
			if capture {
				syntax = parser.OperatorExpr{Name: op.Name, Args: []parser.Expr{raw.Left, raw.Items[i]}, Pos: raw.Pos}
			}
			continue
		}
		properties := mergeExpressionProperties(result.Properties(), pair.Properties())
		result = logicalExpression{expressionInfo: b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties), op: logical, left: result, right: pair}
		if capture {
			syntax = parser.LogicalExpr{Op: logical, Left: syntax, Right: parser.OperatorExpr{Name: op.Name, Args: []parser.Expr{raw.Left, raw.Items[i]}, Pos: raw.Pos}, Pos: raw.Pos}
		}
	}
	if capture {
		if b.inListSyntax == nil {
			b.inListSyntax = make(map[inListSyntaxKey]parser.Expr)
		}
		b.inListSyntax[inListSyntaxKey{frame: b.frameID(), position: raw.Pos}] = syntax
	}
	return result, nil
}

// Copy only changed list storage; the analyzed tree remains immutable.
func mapScalarArrayChildren(n scalarArrayComparisonExpression, child func(BoundExpr) (BoundExpr, bool, error)) (scalarArrayComparisonExpression, bool, error) {
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
	var items []BoundExpr
	for i, old := range n.items {
		next, replaced, err := child(old)
		if err != nil {
			return n, false, err
		}
		if replaced && items == nil {
			items = append([]BoundExpr(nil), n.items...)
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

func (n scalarArrayComparisonExpression) elementType() *sqltype.Descriptor {
	if n.array != nil {
		return n.array.ResultUsage().Type.BaseType().Element()
	}
	return n.items[0].ResultUsage().Type
}

func renderScalarArrayComparison(n scalarArrayComparisonExpression, child func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
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
