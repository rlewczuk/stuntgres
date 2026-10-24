package analyze

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// commonExpressionUsage is also the VALUES authority. The caller supplies the
// release candidate order; unknown remains analysis state, never runtime data.
// An empty label requests PostgreSQL's nonthrowing common-type probe: a category
// mismatch returns an empty Usage, so IN can retain ordinary pairwise resolution.
func CommonExpressionUsage(view catalog.Read, operands []ComparisonOperand, text sqltype.Usage, label string) (sqltype.Usage, error) {
	return selectCommonUsage(view, len(operands), func(i int) ComparisonOperand { return operands[i] }, text, label, (*sqltype.Descriptor).DiagnosticName)
}
func selectCommonUsage(view catalog.Read, count int, operand func(int) ComparisonOperand, text sqltype.Usage, label string, typeName func(*sqltype.Descriptor) string) (sqltype.Usage, error) {
	common, mismatch := sqltype.SelectCommonUsage(count, func(i int) sqltype.Usage {
		o := operand(i)
		if o.descriptor() == nil {
			return sqltype.Usage{}
		}
		return o.Node.ResultUsage()
	}, text, func(source *sqltype.Descriptor, targetID sqltype.ID) bool {
		target, ok := view.LookupType(targetID)
		return ok && catalog.CanCoerce(view, source.Usage(), target.Usage(), sqltype.Implicit) == nil
	})
	if mismatch.Left == nil || label == "" {
		return common, nil
	}
	return sqltype.Usage{}, expr.ErrorAt("42804", fmt.Sprintf("%s types %s and %s cannot be matched", label, typeName(mismatch.Left), typeName(mismatch.Right)), commonOperandStart(operand(mismatch.Index)))
}
func (b *Binder) CoerceConditional(o ComparisonOperand, target sqltype.Usage, label string) (expr.Expr, error) {
	if err := b.CaptureTypeDiagnosticInput(target.Type); err != nil {
		return nil, err
	}
	if target.Type.ContainsCatalogIdentifier() {
		if err := b.captureElementInput(); err != nil {
			return nil, err
		}
	}
	node, err := b.finalizeOperand(o, target.Type)
	if err != nil {
		return nil, err
	}
	if node.ResultUsage().Type == target.Type {
		return node, nil
	}
	if row, ok := node.(plan.RowExpr); ok && row.Usage.Type.ID() == 2249 && target.Type.BaseType().IsComposite() {
		return b.assignCompositeMember(row, target.Type.Usage(), sqltype.Implicit, commonOperandStart(o))
	}
	input, annotations := DomainCoercionInput(node, target.Type.Usage())
	info, err := b.coercionInfo(b.Info(target.Type.Usage(), node.SourcePosition(), expr.Properties{}), input)
	if err != nil {
		return nil, err
	}
	if selected, handled, selectionErr := b.declaredCoercion(input, info.Usage, sqltype.Implicit, input.SourcePosition(), o.raw); handled {
		return RestoreDomainCastCollations(annotations, selected), selectionErr
	}
	coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Implicit)
	if err != nil {
		return nil, expr.ErrorAt("42846", fmt.Sprintf("%s could not convert type %s to %s", label, b.DiagnosticTypeName(node.ResultUsage().Type), b.DiagnosticTypeName(target.Type)), commonOperandStart(o))
	}
	info.Property = CoercionProperties(input, coercionPlan)
	return RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Implicit}), nil
}
func (b *Binder) conditionalEquality(left, right ComparisonOperand, pos parser.Position) (expr.Expr, expr.Expr, sqltype.Comparison, *catalog.Procedure, error) {
	raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}, Pos: pos}
	lt, rt, err := b.ResolveComparisonTypes(raw, left.descriptor(), right.descriptor())
	if err != nil {
		return nil, nil, sqltype.Comparison{}, nil, err
	}
	l, err := b.CoerceConditional(left, lt.Usage(), "=")
	if err != nil {
		return nil, nil, sqltype.Comparison{}, nil, err
	}
	r, err := b.CoerceConditional(right, rt.Usage(), "=")
	if err != nil {
		return nil, nil, sqltype.Comparison{}, nil, err
	}
	input, err := b.comparisonInfo(l, r)
	if err != nil {
		return nil, nil, sqltype.Comparison{}, nil, err
	}
	comparison, err := sqltype.BindComparison(l.ResultUsage().Type, r.ResultUsage().Type, input.Usage.Collation, input.CollationConflict)
	if err != nil {
		return nil, nil, sqltype.Comparison{}, nil, err
	}
	declaration, _, err := b.comparisonRoutineDeclaration("=", l.ResultUsage().Type, r.ResultUsage().Type)
	if err != nil {
		return nil, nil, sqltype.Comparison{}, nil, err
	}
	return l, r, comparison, declaration, nil
}

func (b *Binder) bindConditional(raw parser.ConditionalExpr) (expr.Expr, error) {
	oldRegexp := b.Clause.RegexpClause
	if raw.Kind == parser.Coalesce {
		old := b.Clause.StringSetGuard
		b.Clause.StringSetGuard = "COALESCE"
		b.Clause.RegexpClause = "COALESCE"
		defer func() { b.Clause.StringSetGuard = old }()
	}
	defer func() { b.Clause.RegexpClause = oldRegexp }()
	operands := make([]ComparisonOperand, len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	args := make([]expr.Expr, len(operands))
	var usage sqltype.Usage
	var comparison sqltype.Comparison
	var routine *catalog.Procedure
	missingOrdering := false
	var err error
	if raw.Kind == parser.NullIf {
		args[0], args[1], comparison, routine, err = b.conditionalEquality(operands[0], operands[1], raw.Pos)
		if err != nil {
			return nil, err
		}
		usage = args[0].ResultUsage()
	} else {
		usage, err = selectCommonUsage(b.BindingCatalog(), len(operands), func(i int) ComparisonOperand { return operands[i] }, b.Type(sqltype.Text).Usage(), raw.Kind.String(), b.DiagnosticTypeName)
		if err != nil {
			return nil, err
		}
		for i, o := range operands {
			args[i], err = b.CoerceConditional(o, usage, raw.Kind.String())
			if err != nil {
				return nil, err
			}
		}
	}
	var properties expr.Properties
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if comparison.SessionDependent() {
		properties = expr.MergeProperties(properties, expr.Properties{Volatility: expr.Stable})
	}
	info, err := b.CollatedInfo(b.Info(usage, raw.Pos, properties), args...)
	if err != nil {
		return nil, err
	}
	var routineRef ids.ObjectRef
	if routine != nil {
		info, routineRef, err = b.selectedComparisonRoutineInfo(info, routine)
		if err != nil {
			return nil, err
		}
	}
	if raw.Kind == parser.Greatest || raw.Kind == parser.Least {
		comparison, missingOrdering, err = BindExtremaComparison(info.Usage, info.CollationConflict)
		if err != nil {
			return nil, err
		}
	}
	return plan.ConditionalExpr{Info: info, RoutineRef: routineRef, Kind: raw.Kind, MissingOrdering: missingOrdering, Args: args, Comparison: comparison}, nil
}

func (b *Binder) bindCase(raw parser.CaseExpr) (expr.Expr, error) {
	oldGuard, oldRegexp := b.Clause.StringSetGuard, b.Clause.RegexpClause
	b.Clause.StringSetGuard, b.Clause.RegexpClause = "CASE", "CASE"
	defer func() { b.Clause.StringSetGuard, b.Clause.RegexpClause = oldGuard, oldRegexp }()
	var test expr.Expr
	var err error
	if raw.Test != nil {
		o, err := b.comparisonOperand(raw.Test)
		if err != nil {
			return nil, err
		}
		typ := o.descriptor()
		if typ == nil {
			typ = b.Type(sqltype.Text)
		}
		test, err = b.finalizeOperand(o, typ)
		if err != nil {
			return nil, err
		}
	}
	arms := make([]plan.CaseBranch, len(raw.Arms))
	outputs := make([]ComparisonOperand, len(arms)+1)
	for i, arm := range raw.Arms {
		if test == nil {
			arms[i].Condition, err = b.BindBoolean(arm.Condition, ProjectionContext, "CASE/WHEN")
		} else {
			o, e := b.comparisonOperand(arm.Condition)
			if e != nil {
				return nil, e
			}
			input := plan.CaseTestExpr{Info: b.Info(test.ResultUsage(), test.SourcePosition(), expr.Properties{})}
			arms[i].Test, arms[i].Condition, arms[i].Equality, arms[i].Routine, err = b.conditionalEquality(ComparisonOperand{Node: input}, o, arm.Pos)
			if err == nil {
				_, arms[i].RoutineRef, err = b.selectedComparisonRoutineInfo(expr.Info{}, arms[i].Routine)
				if err != nil {
					return nil, err
				}
				arms[i].TestUsage = arms[i].Test.ResultUsage()
				if selected, selectionErr := catalog.SelectCoercion(b.BindingCatalog(), test.ResultUsage().BaseUsage(), arms[i].TestUsage.BaseUsage(), sqltype.Implicit); selectionErr != nil {
					return nil, selectionErr
				} else if selected != nil {
					arms[i].TestContext = selected.Context
				}
			}
		}
		if err != nil {
			return nil, err
		}
		outputs[i+1], err = b.comparisonOperand(arm.Result)
		if err != nil {
			return nil, err
		}
	}
	otherwise := raw.Else
	if otherwise == nil {
		otherwise = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue, Pos: parser.Position{Offset: -1}}}
	}
	outputs[0], err = b.comparisonOperand(otherwise)
	if err != nil {
		return nil, err
	}
	usage, err := selectCommonUsage(b.BindingCatalog(), len(outputs), func(i int) ComparisonOperand { return outputs[i] }, b.Type(sqltype.Text).Usage(), "CASE", b.DiagnosticTypeName)
	if err != nil {
		return nil, err
	}
	otherwiseNode, err := b.CoerceConditional(outputs[0], usage, "CASE/ELSE")
	if err != nil {
		return nil, err
	}
	var properties expr.Properties
	if test != nil {
		properties = test.Properties()
	}
	properties = expr.MergeProperties(properties, otherwiseNode.Properties())
	for i := range arms {
		arms[i].Result, err = b.CoerceConditional(outputs[i+1], usage, "CASE/WHEN")
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, arms[i].Condition.Properties())
		properties = expr.MergeProperties(properties, arms[i].Result.Properties())
		if test != nil {
			properties = expr.MergeProperties(properties, arms[i].Test.Properties())
		}
		if test != nil && arms[i].Equality.SessionDependent() {
			properties = expr.MergeProperties(properties, expr.Properties{Volatility: expr.Stable})
		}
	}
	info := b.Info(usage.Type.Usage(), raw.Pos, properties)
	info.Usage.Typmod = usage.Typmod
	for _, arm := range arms {
		info, err = b.CollatedInfo(info, arm.Result)
		if err != nil {
			return nil, err
		}
	}
	info, err = b.CollatedInfo(info, otherwiseNode)
	if err != nil {
		return nil, err
	}
	return plan.CaseExpr{Info: info, Test: test, Arms: arms, Otherwise: otherwiseNode}, nil
}
func conditionalName(raw parser.ConditionalExpr) string { return strings.ToLower(raw.Kind.String()) }

func commonOperandStart(o ComparisonOperand) parser.Position {
	if o.raw != nil {
		return parser.ExpressionStart(o.raw)
	}
	return o.Node.SourcePosition()
}
