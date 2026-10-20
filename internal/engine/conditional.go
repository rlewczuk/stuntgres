package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type caseBranch struct {
	condition, result expr.Expr
	// Simple CASE compares a coerced view of its one saved test value.
	testPlan  sqltype.CoercionPlan
	equality  sqltype.Comparison
	testUsage sqltype.Usage
	routine   *catalog.Procedure // Selected equality declaration, never captured permission.
}
type caseExpression struct {
	expr.Info
	test      expr.Expr
	arms      []caseBranch
	otherwise expr.Expr
}

func (e caseExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type == nil || !e.Usage.Type.Concrete() || e.otherwise == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid CASE expression")
	}
	var test sqltype.Value
	var err error
	if e.test != nil {
		test, err = e.test.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for _, arm := range e.arms {
		condition, err := arm.condition.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		match := false
		if e.test != nil {
			view, err := arm.testPlan.Apply(ctx.FunctionContext(), test, ctx.FunctionInput(), ctx.FunctionOutput())
			if err != nil {
				return sqltype.Value{}, err
			}
			if !view.IsNull() && !condition.IsNull() {
				equal, err := arm.equality.EqualValues(ctx.FunctionContext(), view, condition, ctx.FunctionInput())
				if err != nil {
					return sqltype.Value{}, err
				}
				match = equal
			}
		} else if !condition.IsNull() {
			match, err = condition.Boolean()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if match {
			return arm.result.Eval(ctx)
		}
	}
	return e.otherwise.Eval(ctx)
}

type conditionalExpression struct {
	expr.Info
	kind            parser.ConditionalKind
	missingOrdering bool
	args            []expr.Expr
	comparison      sqltype.Comparison
}

func (e conditionalExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type == nil || !e.Usage.Type.Concrete() || len(e.args) == 0 || e.kind > parser.Least || e.kind == parser.NullIf && len(e.args) != 2 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid conditional expression")
	}
	if err := e.extremaError(); err != nil {
		return sqltype.Value{}, err
	}
	if e.kind == parser.NullIf {
		left, err := e.args[0].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		right, err := e.args[1].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if left.Type() != e.Usage.Type || right.Type() != e.args[1].ResultUsage().Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid NULLIF operand value")
		}
		if !left.IsNull() && !right.IsNull() {
			equal, err := e.comparison.EqualValues(ctx.FunctionContext(), left, right, ctx.FunctionInput())
			if err != nil {
				return sqltype.Value{}, err
			}
			if equal {
				return sqltype.Null(e.Usage.Type), nil
			}
		}
		return left, nil
	}
	best := sqltype.Null(e.Usage.Type)
	for _, arg := range e.args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type() != e.Usage.Type {
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
		sign, err := e.comparison.Compare(ctx.FunctionContext(), value, best, ctx.FunctionInput())
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.kind == parser.Greatest && sign > 0 || e.kind == parser.Least && sign < 0 {
			best = value
		}
	}
	return best, nil
}

// Missing ordering is an executable initialization failure, not an equality
// provider or an analysis error: prepared result descriptions remain available.
func (e conditionalExpression) extremaError() error {
	if !e.missingOrdering {
		return nil
	}
	if e.kind != parser.Greatest && e.kind != parser.Least {
		return fmt.Errorf("stuntgres: invalid extrema capability")
	}
	return sqlError("42883", "could not identify a comparison function for type "+e.Usage.Type.DiagnosticName())
}

// commonExpressionUsage is also the VALUES authority. The caller supplies the
// release candidate order; unknown remains analysis state, never runtime data.
// An empty label requests PostgreSQL's nonthrowing common-type probe: a category
// mismatch returns an empty Usage, so IN can retain ordinary pairwise resolution.
func commonExpressionUsage(operands []comparisonOperand, text sqltype.Usage, label string) (sqltype.Usage, error) {
	return selectCommonUsage(len(operands), func(i int) comparisonOperand { return operands[i] }, text, label, (*sqltype.Descriptor).DiagnosticName)
}
func selectCommonUsage(count int, operand func(int) comparisonOperand, text sqltype.Usage, label string, typeName func(*sqltype.Descriptor) string) (sqltype.Usage, error) {
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
		chosen, compatible := sqltype.CommonTypeCandidate(candidate.Type, u.Type)
		if !compatible {
			if label == "" {
				return sqltype.Usage{}, nil
			}
			return sqltype.Usage{}, expr.ErrorAt("42804", fmt.Sprintf("%s types %s and %s cannot be matched", label, typeName(candidate.Type), typeName(u.Type)), commonOperandStart(o))
		}
		if chosen == u.Type {
			candidate = u
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
func (b *expressionBinder) coerceConditional(o comparisonOperand, target sqltype.Usage, label string) (expr.Expr, error) {
	if err := b.captureTypeDiagnosticInput(target.Type); err != nil {
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
	if row, ok := node.(rowExpression); ok && row.Usage.Type.ID() == 2249 && target.Type.BaseType().IsComposite() {
		return b.assignCompositeMember(row, target.Type.Usage(), sqltype.Implicit, commonOperandStart(o))
	}
	input, annotations := domainCoercionInput(node, target.Type.Usage())
	info, err := b.coercionInfo(b.info(target.Type.Usage(), node.SourcePosition(), expr.Properties{}), input)
	if err != nil {
		return nil, err
	}
	plan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Implicit)
	if err != nil {
		return nil, expr.ErrorAt("42846", fmt.Sprintf("%s could not convert type %s to %s", label, b.diagnosticTypeName(node.ResultUsage().Type), b.diagnosticTypeName(target.Type)), commonOperandStart(o))
	}
	info.Property = coercionProperties(input, plan)
	return restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, sqltype.Implicit, false}), nil
}
func (b *expressionBinder) conditionalEquality(left, right comparisonOperand, pos parser.Position) (expr.Expr, expr.Expr, sqltype.Comparison, *catalog.Procedure, error) {
	raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}, Pos: pos}
	lt, rt, err := b.resolveComparisonTypes(raw, left.descriptor(), right.descriptor())
	if err != nil {
		return nil, nil, sqltype.Comparison{}, nil, err
	}
	l, err := b.coerceConditional(left, lt.Usage(), "=")
	if err != nil {
		return nil, nil, sqltype.Comparison{}, nil, err
	}
	r, err := b.coerceConditional(right, rt.Usage(), "=")
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
	routine, _, err := b.captureBuiltinRoutineDeclaration(declaration)
	return l, r, comparison, routine, err
}

// Extrema retain ordering authority separately from scalar equality. Identifier
// types without ordering remain describable, but never acquire a fake comparator.
func bindExtremaComparison(usage sqltype.Usage, indeterminate bool) (sqltype.Comparison, bool, error) {
	// Array SQL comparison does not expand the independently bounded extrema
	// consumer. Preserve its previous analysis failure, including domains.
	if usage.Type.BaseType().IsArray() {
		return sqltype.Comparison{}, false, sqlError("42883", "incompatible comparison operands")
	}
	if usage.Type.BaseType().IsEnum() || sqltype.IsTransactionIdentifier(usage.Type.BaseType().ID()) {
		comparison, err := sqltype.BindOrdering(usage)
		if err != nil && (usage.Type.BaseType().ID() == sqltype.Xid || usage.Type.BaseType().ID() == sqltype.Cid) {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42883" {
				return sqltype.Comparison{}, true, nil
			}
		}
		return comparison, false, err
	}
	comparison, err := sqltype.BindComparison(usage.Type, usage.Type, usage.Collation, indeterminate)
	return comparison, false, err
}

func (b *expressionBinder) bindConditional(raw parser.ConditionalExpr) (expr.Expr, error) {
	oldRegexp := b.clause.regexpClause
	if raw.Kind == parser.Coalesce {
		old := b.clause.stringSetGuard
		b.clause.stringSetGuard = "COALESCE"
		b.clause.regexpClause = "COALESCE"
		defer func() { b.clause.stringSetGuard = old }()
	}
	defer func() { b.clause.regexpClause = oldRegexp }()
	operands := make([]comparisonOperand, len(raw.Args))
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
	}
	var properties expr.Properties
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if comparison.SessionDependent() {
		properties = expr.MergeProperties(properties, expr.Properties{Volatility: expr.Stable})
	}
	info, err := b.collatedInfo(b.info(usage, raw.Pos, properties), args...)
	if err != nil {
		return nil, err
	}
	if routine != nil {
		info.RoutineReference = routine
	}
	if raw.Kind == parser.Greatest || raw.Kind == parser.Least {
		comparison, missingOrdering, err = bindExtremaComparison(info.Usage, info.CollationConflict)
		if err != nil {
			return nil, err
		}
	}
	return conditionalExpression{info, raw.Kind, missingOrdering, args, comparison}, nil
}

// CASE compares the once-saved test datum through an implicit CaseTestExpr.
// Its type and selected capability survive, but explicit derivation does not.
type implicitComparisonOperand struct{ expr.Expr }

func (implicitComparisonOperand) ExplicitCollation() parser.Position { return parser.Position{} }

func (b *expressionBinder) bindCase(raw parser.CaseExpr) (expr.Expr, error) {
	oldGuard, oldRegexp := b.clause.stringSetGuard, b.clause.regexpClause
	b.clause.stringSetGuard, b.clause.regexpClause = "CASE", "CASE"
	defer func() { b.clause.stringSetGuard, b.clause.regexpClause = oldGuard, oldRegexp }()
	var test expr.Expr
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
			var view expr.Expr
			view, arms[i].condition, arms[i].equality, arms[i].routine, err = b.conditionalEquality(comparisonOperand{node: implicitComparisonOperand{test}, raw: raw.Test}, o, arm.Pos)
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
	var properties expr.Properties
	if test != nil {
		properties = test.Properties()
	}
	properties = expr.MergeProperties(properties, otherwiseNode.Properties())
	for i := range arms {
		arms[i].result, err = b.coerceConditional(outputs[i+1], usage, "CASE/WHEN")
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, arms[i].condition.Properties())
		properties = expr.MergeProperties(properties, arms[i].result.Properties())
		if test != nil && (arms[i].testPlan.SessionDependent() || arms[i].equality.SessionDependent()) {
			properties = expr.MergeProperties(properties, expr.Properties{Volatility: expr.Stable})
		}
	}
	info := b.info(usage.Type.Usage(), raw.Pos, properties)
	info.Usage.Typmod = usage.Typmod
	for _, arm := range arms {
		info, err = b.collatedInfo(info, arm.result)
		if err != nil {
			return nil, err
		}
	}
	info, err = b.collatedInfo(info, otherwiseNode)
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
			return expr.ErrorAt("0A000", "qualified column references are unsupported", e.Pos)
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
	case parser.GroupingExpr:
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
		if e.Filter != nil {
			return qualifiedCastChild(e.Filter)
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
	case parser.FieldExpr:
		return qualifiedCastChild(e.Base)
	case parser.RowExpr:
		for _, field := range e.Fields {
			if err := qualifiedCastChild(field); err != nil {
				return err
			}
		}
	case parser.InListExpr:
		if err := qualifiedCastChild(e.Left); err != nil {
			return err
		}
		for _, item := range e.Items {
			if err := qualifiedCastChild(item); err != nil {
				return err
			}
		}
	case parser.ArrayComparisonExpr:
		if err := qualifiedCastChild(e.Left); err != nil {
			return err
		}
		return qualifiedCastChild(e.Right)
	case parser.ArrayExpr:
		for _, element := range e.Elements {
			if err := qualifiedCastChild(element); err != nil {
				return err
			}
		}
	case parser.SubscriptExpr:
		var storage [13]parser.Expr
		for _, child := range e.Children(&storage) {
			if err := qualifiedCastChild(child); err != nil {
				return err
			}
		}
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
	case parser.SQLJSONExpr:
		for _, arg := range e.Args {
			if err := qualifiedCastChild(arg); err != nil {
				return err
			}
		}
	case parser.JSONPredicateExpr:
		return qualifiedCastChild(e.Child)
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
