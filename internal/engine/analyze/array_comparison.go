package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Resolve the operator before finalizing unknown occurrences. The returned
// targets are the selected operation's actual inputs, not the source pair.
func (b *Binder) ResolveComparisonTypes(raw parser.OperatorExpr, lt, rt *sqltype.Descriptor) (*sqltype.Descriptor, *sqltype.Descriptor, error) {
	return b.resolveComparisonCandidates(raw, lt, rt, nil)
}

func (b *Binder) resolveComparisonCandidates(raw parser.OperatorExpr, lt, rt *sqltype.Descriptor, candidates *operatorCandidateSet) (*sqltype.Descriptor, *sqltype.Descriptor, error) {
	// Internal provider dispatch has already selected and coerced these inputs.
	if len(raw.Args) == 2 && len(b.Clause.SelectedOperatorArgs) == 2 {
		left, lok := raw.Args[0].(parser.ParameterExpr)
		right, rok := raw.Args[1].(parser.ParameterExpr)
		if lok && rok && left.Number == -1 && right.Number == -2 {
			return lt, rt, nil
		}
	}
	operator, ok := ComparisonOperator(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
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
		if err := b.requireBuiltinComparison(selected, raw.Pos); err != nil {
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
func (b *Binder) comparisonInputType(id sqltype.ID, own, other *sqltype.Descriptor) *sqltype.Descriptor {
	polymorphic := !b.Type(id).Concrete() && id != 2249
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
	return b.Type(id)
}

func (b *Binder) bindArrayComparison(raw parser.ArrayComparisonExpr) (expr.Expr, error) {
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
	operator, ok := ComparisonOperator(name)
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
		if err := b.requireBuiltinComparison(selected, raw.Pos); err != nil {
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
	l, err := b.CoerceConditional(left, lt.Usage(), name)
	if err != nil {
		return nil, err
	}
	var target sqltype.Usage
	if right.Node != nil && element != nil && element.BaseType() == rt {
		// The existing RHS already carries the resolved physical element.
		// Retain its exact domain/array generations, typmod and collation;
		// an array-valued element need not have an array-of-array provider.
		target = right.Node.ResultUsage()
	} else {
		arrayType, found := b.definitionArray(rt)
		if !found || !arrayType.Concrete() {
			return nil, expr.ErrorAt("0A000", "unsupported array element type", raw.Pos)
		}
		target = arrayType.Usage()
	}
	r, err := b.CoerceConditional(right, target, name)
	if err != nil {
		return nil, err
	}
	input, err := b.comparisonInfo(l, r)
	if err != nil {
		return nil, err
	}
	predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, r.ResultUsage().Type.BaseType().Element(), b.Type(sqltype.Bool), input.Usage.Collation, input.CollationConflict)
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
	info := b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties)
	var routineRef ids.ObjectRef
	if declaration != nil {
		info, routineRef, err = b.selectedComparisonRoutineInfo(info, declaration)
		if err != nil {
			return nil, err
		}
	}
	return plan.ScalarArrayComparisonExpr{Info: info, RoutineRef: routineRef, Left: l, Array: r, Predicate: predicate, Operator: name, Namespace: namespace, All: raw.All}, nil
}

func (b *Binder) InListHasVar(node expr.Expr) bool {
	found := false
	frame := b.FrameID()
	var visit func(expr.Expr)
	visit = func(child expr.Expr) {
		switch n := child.(type) {
		case plan.ColumnExpr:
			found = found || n.Frame == frame
		case plan.ReturningWholeRowExpr:
			found = found || n.Frame == frame
		case plan.AggregateExpr:
			if n.Frame == frame {
				if call, ok := b.aggregateCallAt(n); ok {
					call.Expressions(func(expr expr.Expr) { WalkQueryArgumentExpression(expr, visit) })
				}
			}
		}
	}
	WalkQueryArgumentExpression(node, visit)
	return found
}

type StoredExpressionSyntaxKey struct {
	Frame    uint32
	Position parser.Position
}

func (b *Binder) bindInList(raw parser.InListExpr) (expr.Expr, error) {
	if len(raw.Items) == 0 {
		return nil, fmt.Errorf("stuntgres: empty IN list")
	}
	leftInput, err := b.inComparisonOperand(raw.Left)
	if err != nil {
		return nil, err
	}
	items := make([]ComparisonOperand, len(raw.Items))
	var rowItems []inComparisonOperand
	if leftInput.constructor {
		rowItems = make([]inComparisonOperand, len(raw.Items))
	}
	nonvar := make([]ComparisonOperand, 1, len(raw.Items)+1)
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
		vars[i] = b.InListHasVar(items[i].Node)
		if leftInput.constructor && rowItems[i].constructor && items[i].Node == nil {
			for _, field := range rowItems[i].fields {
				vars[i] = vars[i] || b.InListHasVar(field.Node)
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
	operator, _ := ComparisonOperator(name)
	op := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: raw.Pos}}}, Pos: raw.Pos}
	var result expr.Expr
	capture := b.Params.NoParameters || b.Consumer.frozenDefinition
	var syntax parser.Expr
	batched := false
	if len(nonvar) > 2 {
		common, err := CommonExpressionUsage(b.BindingCatalog(), nonvar, b.Type(sqltype.Text).Usage(), "")
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
				if err := catalog.CanCoerce(b.BindingCatalog(), item.Node.ResultUsage(), common, sqltype.Implicit); err != nil {
					feasible = false
					break
				}
			}
		}
		if feasible {
			children := make([]expr.Expr, len(nonvar)-1)
			properties := expr.Properties{}
			for i, item := range nonvar[1:] {
				children[i], err = b.CoerceConditional(item, common, "IN")
				if err != nil {
					return nil, err
				}
			}
			// The batch array inputs are finalized before scalar operator lookup
			// and lhs input, matching transformAExprIn's diagnostic precedence.
			leftTarget, rightTarget, err := b.ResolveComparisonTypes(op, nonvar[0].descriptor(), common.Type)
			if err != nil {
				return nil, err
			}
			declaration, _, err := b.comparisonRoutineDeclaration(name, leftTarget, rightTarget)
			if err != nil {
				return nil, err
			}
			for i, child := range children {
				children[i], err = b.CoerceConditional(ComparisonOperand{Node: child}, rightTarget.Usage(), name)
				if err != nil {
					return nil, err
				}
				properties = expr.MergeProperties(properties, children[i].Properties())
			}
			l, err := b.CoerceConditional(left, leftTarget.Usage(), name)
			if err != nil {
				return nil, err
			}
			properties = expr.MergeProperties(properties, l.Properties())
			input, err := b.CollatedInfo(b.Info(rightTarget.Usage(), raw.Pos, properties), children...)
			if err != nil {
				return nil, err
			}
			input, err = b.CollatedInfo(input, l)
			if err != nil {
				return nil, err
			}
			predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, children[0].ResultUsage().Type, b.Type(sqltype.Bool), input.Usage.Collation, input.CollationConflict)
			if err != nil {
				return nil, functions.ComparisonError(b, err, left.descriptor(), common.Type, op)
			}
			if predicate.SessionDependent() && properties.Volatility < expr.Stable {
				properties.Volatility = expr.Stable
			}
			info := b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties)
			var routineRef ids.ObjectRef
			if declaration != nil {
				info, routineRef, err = b.selectedComparisonRoutineInfo(info, declaration)
				if err != nil {
					return nil, err
				}
			}
			result = plan.ScalarArrayComparisonExpr{Info: info, RoutineRef: routineRef, Left: l, Items: children, Predicate: predicate, Operator: name, All: raw.Negate}
			// PostgreSQL coerces an unknown Param occurrence in place, unlike
			// an unknown Const. Only the original batch lhs is updated before
			// Var pairs copy it; other transformed occurrences stay independent.
			if parameter, ok := left.Node.(plan.ParameterExpr); ok && parameter.Usage.Type == nil {
				left.Node = l
			}
			batched = true
			if capture {
				elements := make([]parser.Expr, 0, len(children))
				for i, item := range raw.Items {
					if !vars[i] {
						elements = append(elements, item)
					}
				}
				if leftTarget.IsCube() {
					// The internal batch owns scalar children, not a public cube
					// array. Keep its IN recipe executable through persistence.
					syntax = parser.InListExpr{Left: raw.Left, Items: elements, Negate: raw.Negate, Pos: raw.Pos}
				} else {
					syntax = parser.ArrayComparisonExpr{Left: raw.Left, Right: parser.ArrayExpr{Elements: elements, Pos: raw.Pos}, Name: op.Name, All: raw.Negate, Pos: raw.Pos}
				}
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
			if left.Node == nil && leftInput.constructor {
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
			pair, err = b.BindComparisonOperands(op, operator, left, item)
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
				comparison := pair.(plan.RowComparisonExpr)
				for side, input := range [2]inComparisonOperand{leftInput, rowItems[i]} {
					_, rawRow := input.scalar.raw.(parser.RowExpr)
					if constructor, ok := input.scalar.Node.(plan.RowExpr); ok && !rawRow {
						args[side] = *constructor.Raw
						comparison.MemberCasts[side] = true
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
		result = plan.LogicalExpr{Info: b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), Op: logical, Left: result, Right: pair}
		if capture {
			syntax = parser.LogicalExpr{Op: logical, Left: syntax, Right: pairSyntax, Pos: raw.Pos}
		}
	}
	if capture {
		if b.Stored.Syntax == nil {
			b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
		}
		b.Stored.Syntax[b.storedExpressionSyntaxKey(raw)] = syntax
	}
	return result, nil
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
