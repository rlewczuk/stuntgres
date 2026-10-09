package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type caseBranch struct {
	condition, result BoundExpr
	// Simple CASE compares a coerced view of its one saved test value.
	testPlan  sqltype.CoercionPlan
	equality  sqltype.Comparison
	testUsage sqltype.Usage
}
type caseExpression struct {
	expressionInfo
	test      BoundExpr
	arms      []caseBranch
	otherwise BoundExpr
}

func (e caseExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.usage.Type == nil || !e.usage.Type.Concrete() || e.otherwise == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid CASE expression")
	}
	var test sqltype.Value
	var err error
	if e.test != nil {
		test, err = e.test.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for _, arm := range e.arms {
		condition, err := arm.condition.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		match := false
		if e.test != nil {
			view, err := arm.testPlan.Apply(c.Context, test, c.Input, c.Output)
			if err != nil {
				return sqltype.Value{}, err
			}
			if !view.IsNull() && !condition.IsNull() {
				sign, err := arm.equality.Compare(c.Context, view, condition, c.Input)
				if err != nil {
					return sqltype.Value{}, err
				}
				match = sign == 0
			}
		} else if !condition.IsNull() {
			match, err = condition.Boolean()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if match {
			return arm.result.Eval(c)
		}
	}
	return e.otherwise.Eval(c)
}

type conditionalExpression struct {
	expressionInfo
	kind       parser.ConditionalKind
	args       []BoundExpr
	comparison sqltype.Comparison
}

func (e conditionalExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.usage.Type == nil || !e.usage.Type.Concrete() || len(e.args) == 0 || e.kind > parser.Least || e.kind == parser.NullIf && len(e.args) != 2 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid conditional expression")
	}
	if e.kind == parser.NullIf {
		left, err := e.args[0].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		right, err := e.args[1].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if left.Type() != e.usage.Type || right.Type() != e.args[1].ResultUsage().Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid NULLIF operand value")
		}
		if !left.IsNull() && !right.IsNull() {
			if e.CollationIndeterminate() {
				return sqltype.Value{}, conditionalCollationError()
			}
			sign, err := e.comparison.Compare(c.Context, left, right, c.Input)
			if err != nil {
				return sqltype.Value{}, err
			}
			if sign == 0 {
				return sqltype.Null(e.usage.Type), nil
			}
		}
		return left, nil
	}
	best := sqltype.Null(e.usage.Type)
	for _, arg := range e.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type() != e.usage.Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid conditional argument value")
		}
		if value.IsNull() {
			continue
		}
		if e.kind == parser.Coalesce {
			return value, nil
		}
		if best.IsNull() {
			best = value
			continue
		}
		if e.CollationIndeterminate() {
			return sqltype.Value{}, conditionalCollationError()
		}
		sign, err := e.comparison.Compare(c.Context, value, best, c.Input)
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.kind == parser.Greatest && sign > 0 || e.kind == parser.Least && sign < 0 {
			best = value
		}
	}
	return best, nil
}
func mergeExpressionProperties(a, b ExprProperties) ExprProperties {
	if b.Volatility > a.Volatility {
		a.Volatility = b.Volatility
	}
	a.RowDependent = a.RowDependent || b.RowDependent
	a.ParameterDependent = a.ParameterDependent || b.ParameterDependent
	a.StageDependent = a.StageDependent || b.StageDependent
	return a
}

func conditionalCollationError() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
}

// commonExpressionUsage is also the VALUES authority. The caller supplies the
// release candidate order; unknown remains analysis state, never runtime data.
func commonExpressionUsage(operands []comparisonOperand, text sqltype.Usage, label string) (sqltype.Usage, error) {
	return selectCommonUsage(len(operands), func(i int) comparisonOperand { return operands[i] }, text, label)
}
func selectCommonUsage(count int, operand func(int) comparisonOperand, text sqltype.Usage, label string) (sqltype.Usage, error) {
	var domain *sqltype.Descriptor
	allDomain := count > 0
	for i := range count {
		d := operand(i).descriptor()
		if d == nil || !d.IsDomain() || domain != nil && domain != d {
			allDomain = false
			break
		}
		domain = d
	}
	if allDomain {
		return operand(0).node.ResultUsage(), nil
	}
	var candidate sqltype.Usage
	typmod := int32(-1)
	sameMod := true
	for i := range count {
		o := operand(i)
		if o.descriptor() == nil {
			sameMod = false
			continue
		}
		u := o.node.ResultUsage()
		if u.Type.IsDomain() {
			u.Type = u.Type.BaseType()
			u.Typmod = -1
			sameMod = false
		}
		if candidate.Type == nil {
			candidate = u
			typmod = u.Typmod
			continue
		}
		if u.Type != candidate.Type || u.Typmod != typmod {
			sameMod = false
		}
		if u.Type == candidate.Type {
			continue
		}
		if typeCategory(candidate.Type) != typeCategory(u.Type) {
			return sqltype.Usage{}, expressionError("42804", fmt.Sprintf("%s types %s and %s cannot be matched", label, candidate.Type.DiagnosticName(), u.Type.DiagnosticName()), commonOperandStart(o))
		}
		if !preferredType(candidate.Type) {
			_, forward := sqltype.PlanCoercion(candidate.Type.Usage(), u.Type.Usage(), sqltype.Implicit)
			_, reverse := sqltype.PlanCoercion(u.Type.Usage(), candidate.Type.Usage(), sqltype.Implicit)
			if forward == nil && reverse != nil {
				candidate = u
			}
		}
	}
	if candidate.Type == nil {
		return text, nil
	}
	if sameMod {
		candidate.Typmod = typmod
	} else {
		candidate.Typmod = -1
	}
	return candidate, nil
}
func (b *expressionBinder) coerceConditional(o comparisonOperand, target sqltype.Usage, label string) (BoundExpr, error) {
	node, err := b.finalizeOperand(o, target.Type)
	if err != nil {
		return nil, err
	}
	if node.ResultUsage().Type == target.Type {
		return node, nil
	}
	info, err := collatedInfo(b.info(target.Type.Usage(), node.SourcePosition(), ExprProperties{}), node)
	if err != nil {
		return nil, err
	}
	plan, err := sqltype.PlanCoercion(node.ResultUsage(), info.usage, sqltype.Implicit)
	if err != nil {
		return nil, expressionError("42846", fmt.Sprintf("%s could not convert type %s to %s", label, node.ResultUsage().Type.DiagnosticName(), target.Type.DiagnosticName()), commonOperandStart(o))
	}
	info.properties = coercionProperties(node, plan)
	return coercionExpression{info, node, plan, sqltype.Implicit}, nil
}
func equalityError(err error, left, right *sqltype.Descriptor, pos parser.Position) error {
	d, ok := err.(*sqlstate.Error)
	if !ok {
		return err
	}
	name := func(t *sqltype.Descriptor) string {
		if t == nil {
			return "unknown"
		}
		return t.DiagnosticName()
	}
	message := fmt.Sprintf("operator does not exist: %s = %s", name(left), name(right))
	hint := "No operator matches the given name and argument types. You might need to add explicit type casts."
	if d.Code == "42725" {
		message = fmt.Sprintf("operator is not unique: %s = %s", name(left), name(right))
		hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
	}
	copy := expressionError(d.Code, message, pos).(*sqlstate.Error)
	copy.Hint = hint
	return copy
}
func (b *expressionBinder) conditionalEquality(left, right comparisonOperand, pos parser.Position) (BoundExpr, BoundExpr, sqltype.Comparison, error) {
	s, err := sqltype.ResolveEquality(left.descriptor(), right.descriptor())
	if err != nil {
		return nil, nil, sqltype.Comparison{}, equalityError(err, left.descriptor(), right.descriptor(), pos)
	}
	l, err := b.coerceConditional(left, b.typ(s.Left).Usage(), "=")
	if err != nil {
		return nil, nil, sqltype.Comparison{}, err
	}
	r, err := b.coerceConditional(right, b.typ(s.Right).Usage(), "=")
	if err != nil {
		return nil, nil, sqltype.Comparison{}, err
	}
	comparison, err := sqltype.BindEqualityComparison(l.ResultUsage().Type, r.ResultUsage().Type)
	return l, r, comparison, err
}
func (b *expressionBinder) bindConditional(raw parser.ConditionalExpr) (BoundExpr, error) {
	operands := make([]comparisonOperand, len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	args := make([]BoundExpr, len(operands))
	var usage sqltype.Usage
	var comparison sqltype.Comparison
	var err error
	if raw.Kind == parser.NullIf {
		args[0], args[1], comparison, err = b.conditionalEquality(operands[0], operands[1], raw.Pos)
		if err != nil {
			return nil, err
		}
		usage = args[0].ResultUsage()
	} else {
		usage, err = commonExpressionUsage(operands, b.typ(sqltype.Text).Usage(), raw.Kind.String())
		if err != nil {
			return nil, err
		}
		for i, o := range operands {
			args[i], err = b.coerceConditional(o, usage, raw.Kind.String())
			if err != nil {
				return nil, err
			}
		}
		if raw.Kind != parser.Coalesce {
			comparison, err = sqltype.BindComparison(usage.Type, usage.Type)
			if err != nil {
				return nil, err
			}
		}
	}
	var properties ExprProperties
	for _, arg := range args {
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	if comparison.SessionDependent() {
		properties = mergeExpressionProperties(properties, ExprProperties{Volatility: Stable})
	}
	info, err := collatedInfo(b.info(usage, raw.Pos, properties), args...)
	if err != nil {
		return nil, err
	}
	return conditionalExpression{info, raw.Kind, args, comparison}, nil
}
func (b *expressionBinder) bindCase(raw parser.CaseExpr) (BoundExpr, error) {
	var test BoundExpr
	var err error
	if raw.Test != nil {
		o, err := b.comparisonOperand(raw.Test)
		if err != nil {
			return nil, err
		}
		typ := o.descriptor()
		if typ == nil {
			typ = b.typ(sqltype.Text)
		}
		test, err = b.finalizeOperand(o, typ)
		if err != nil {
			return nil, err
		}
	}
	arms := make([]caseBranch, len(raw.Arms))
	outputs := make([]comparisonOperand, len(arms)+1)
	for i, arm := range raw.Arms {
		if test == nil {
			arms[i].condition, err = b.bindBoolean(arm.Condition, projectionContext, "CASE/WHEN")
		} else {
			o, e := b.comparisonOperand(arm.Condition)
			if e != nil {
				return nil, e
			}
			var view BoundExpr
			view, arms[i].condition, arms[i].equality, err = b.conditionalEquality(comparisonOperand{node: test, raw: raw.Test}, o, arm.Pos)
			if err == nil {
				arms[i].testUsage = view.ResultUsage()
				arms[i].testPlan, err = sqltype.PlanCoercion(test.ResultUsage(), view.ResultUsage(), sqltype.Implicit)
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
	usage, err := commonExpressionUsage(outputs, b.typ(sqltype.Text).Usage(), "CASE")
	if err != nil {
		return nil, err
	}
	otherwiseNode, err := b.coerceConditional(outputs[0], usage, "CASE/ELSE")
	if err != nil {
		return nil, err
	}
	var properties ExprProperties
	if test != nil {
		properties = test.Properties()
	}
	properties = mergeExpressionProperties(properties, otherwiseNode.Properties())
	for i := range arms {
		arms[i].result, err = b.coerceConditional(outputs[i+1], usage, "CASE/WHEN")
		if err != nil {
			return nil, err
		}
		properties = mergeExpressionProperties(properties, arms[i].condition.Properties())
		properties = mergeExpressionProperties(properties, arms[i].result.Properties())
		if test != nil && (arms[i].testPlan.SessionDependent() || arms[i].equality.SessionDependent()) {
			properties = mergeExpressionProperties(properties, ExprProperties{Volatility: Stable})
		}
	}
	info := b.info(usage.Type.Usage(), raw.Pos, properties)
	info.usage.Typmod = usage.Typmod
	for _, arm := range arms {
		info, err = collatedInfo(info, arm.result)
		if err != nil {
			return nil, err
		}
	}
	info, err = collatedInfo(info, otherwiseNode)
	if err != nil {
		return nil, err
	}
	return caseExpression{info, test, arms, otherwiseNode}, nil
}
func conditionalName(raw parser.ConditionalExpr) string { return strings.ToLower(raw.Kind.String()) }

// Legacy nonquery cast contexts (including DELETE WHERE) exclude qualification.
// Conditional nesting must not smuggle references through that scalar atom seam.
func qualifiedCastChild(raw parser.Expr) error {
	if raw == nil {
		return nil
	}
	switch e := raw.(type) {
	case parser.ColumnExpr:
		if len(e.Name.Parts) > 1 {
			return expressionError("0A000", "qualified column references are unsupported", e.Pos)
		}
	case parser.CaseExpr:
		if err := qualifiedCastChild(e.Test); err != nil {
			return err
		}
		for _, arm := range e.Arms {
			if err := qualifiedCastChild(arm.Condition); err != nil {
				return err
			}
			if err := qualifiedCastChild(arm.Result); err != nil {
				return err
			}
		}
		return qualifiedCastChild(e.Else)
	case parser.ConditionalExpr:
		for _, arg := range e.Args {
			if err := qualifiedCastChild(arg); err != nil {
				return err
			}
		}
	case parser.AggregateExpr:
		for _, arg := range e.Args {
			if err := qualifiedCastChild(arg); err != nil {
				return err
			}
		}
		for _, key := range e.Order {
			if err := qualifiedCastChild(key.Expr); err != nil {
				return err
			}
		}
	case parser.CollateExpr:
		return qualifiedCastChild(e.Child)
	case parser.CastExpr:
		return qualifiedCastChild(e.Child)
	case parser.TimeZoneExpr:
		if child := qualifiedCastChild(e.Value); child != nil {
			return child
		}
		if !e.Local {
			return qualifiedCastChild(e.Zone)
		}
	case parser.RegclassExpr:
		return qualifiedCastChild(e.Child)
	case parser.OperatorExpr:
		for _, arg := range e.Args {
			if err := qualifiedCastChild(arg); err != nil {
				return err
			}
		}
	case parser.CallExpr:
		for _, arg := range e.Args {
			if err := qualifiedCastChild(arg); err != nil {
				return err
			}
		}
	case parser.FunctionExpr:
		for _, arg := range e.Args {
			if err := qualifiedCastChild(arg); err != nil {
				return err
			}
		}
	case parser.LogicalExpr:
		if err := qualifiedCastChild(e.Left); err != nil {
			return err
		}
		return qualifiedCastChild(e.Right)
	case parser.PredicateExpr:
		if err := qualifiedCastChild(e.Left); err != nil {
			return err
		}
		return qualifiedCastChild(e.Right)
	}
	return nil
}

func commonOperandStart(o comparisonOperand) parser.Position {
	if o.raw != nil {
		return parser.ExpressionStart(o.raw)
	}
	return o.node.SourcePosition()
}
