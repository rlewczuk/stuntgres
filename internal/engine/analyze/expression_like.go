package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func likeName(name string) bool {
	return name == "~~" || name == "!~~" || name == "~~*" || name == "!~~*"
}

func LikeProperties(a, b expr.Expr) expr.Properties {
	p, q := a.Properties(), b.Properties()
	p.Volatility = max(p.Volatility, q.Volatility)
	p.RowDependent = p.RowDependent || q.RowDependent
	p.ParameterDependent = p.ParameterDependent || q.ParameterDependent
	p.StageDependent = p.StageDependent || q.StageDependent
	p.DomainDependent = p.DomainDependent || q.DomainDependent
	p.RuntimeDomainConversion = p.RuntimeDomainConversion || q.RuntimeDomainConversion
	return p
}

func (b *Binder) CollatedInfo(info expr.Info, inputs ...expr.Expr) (expr.Info, error) {
	return CollatedInfo(b.BindingCatalog(), info, inputs...)
}

func (b *Binder) coercionInfo(info expr.Info, child expr.Expr) (expr.Info, error) {
	info, err := CoercionInfo(b.BindingCatalog(), info, child)
	if err != nil {
		return expr.Info{}, err
	}
	return b.coercionRoutineInfo(info, child)
}

// Some successful coercion seams deliberately retain their own collation
// handling. They still select the same actual function-method identity.
func (b *Binder) coercionRoutineInfo(info expr.Info, child expr.Expr) (expr.Info, error) {
	if id := routine.EmbeddedCastOID(child.ResultUsage().Type, info.Usage.Type); id != 0 {
		return b.selectedBuiltinRoutineInfo(info, routine.BuiltinRoutine(id))
	}
	return info, nil
}

func comparisonInfo(view catalog.Read, left, right expr.Expr) (expr.Info, error) {
	usage := left.ResultUsage()
	if usage.Collation.ID == 0 {
		usage = right.ResultUsage()
	}
	return CollatedInfo(view, expr.Info{Usage: usage}, left, right)
}

func (b *Binder) comparisonInfo(left, right expr.Expr) (expr.Info, error) {
	if err := b.captureComparisonDiagnosticInput(left.ResultUsage().Type); err != nil {
		return expr.Info{}, err
	}
	if err := b.captureComparisonDiagnosticInput(right.ResultUsage().Type); err != nil {
		return expr.Info{}, err
	}
	return comparisonInfo(b.BindingCatalog(), left, right)
}

func (b *Binder) likeOperand(o ComparisonOperand, target *sqltype.Descriptor) (expr.Expr, error) {
	node, err := b.finalizeOperand(o, target)
	if err != nil {
		return nil, err
	}
	if node.ResultUsage().Type == target {
		return node, nil
	}
	u := target.Usage()
	if u.Collation.ID != 0 && node.ResultUsage().Collation.ID != 0 {
		u.Collation = node.ResultUsage().Collation
	}
	coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), u, sqltype.Implicit)
	if err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.Info(u, node.SourcePosition(), CoercionProperties(node, coercionPlan)), node)
	if err != nil {
		return nil, err
	}
	return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}, nil
}

func bindLike(b functions.Binder, raw parser.OperatorExpr) (expr.Expr, error) {
	if len(raw.Args) != 2 || len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid LIKE expression")
	}
	left, err := b.AnalyzeArgument(raw.Args[0], functions.AnalysisRequest{Intent: functions.ComparisonArgument})
	if err != nil {
		return nil, err
	}
	right, err := b.AnalyzeArgument(raw.Args[1], functions.AnalysisRequest{Intent: functions.ComparisonArgument})
	if err != nil {
		return nil, err
	}
	visible, err := b.BuiltinOperatorVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	missing := func() (expr.Expr, error) {
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), left.Usage.Type, right.Usage.Type, raw)
	}
	if !visible {
		return missing()
	}
	operator := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	lt, rt := left.Usage.Type, right.Usage.Type
	if lt != nil {
		lt = lt.BaseType()
	}
	if rt != nil {
		rt = rt.BaseType()
	}
	lhs, rhs := b.Type(sqltype.Text), b.Type(sqltype.Text)
	if lt != nil && lt.ID() == sqltype.Bytea || lt == nil && rt != nil && rt.ID() == sqltype.Bytea {
		if operator == "~~*" || operator == "!~~*" {
			return missing()
		}
		lhs, rhs = b.Type(sqltype.Bytea), b.Type(sqltype.Bytea)
	} else if lt != nil && (lt.ID() == sqltype.Name || lt.ID() == sqltype.BPChar) {
		lhs = lt
	}
	valid := func(d *sqltype.Descriptor, binary bool) bool {
		if d == nil {
			return true
		}
		if binary {
			return d.ID() == sqltype.Bytea
		}
		return d.ID() == sqltype.Text || d.ID() == sqltype.Name || d.ID() == sqltype.BPChar || d.ID() == sqltype.VarChar
	}
	if !valid(lt, lhs.ID() == sqltype.Bytea) || !valid(rt, rhs.ID() == sqltype.Bytea) {
		return missing()
	}
	l, err := b.CoerceArgument(left, functions.CoercionRequest{Intent: functions.LikeArgument, Target: lhs.Usage()})
	if err != nil {
		return nil, err
	}
	r, err := b.CoerceArgument(right, functions.CoercionRequest{Intent: functions.LikeArgument, Target: rhs.Usage()})
	if err != nil {
		return nil, err
	}
	collation, err := b.CollatedInfo(b.Info(l.ResultUsage(), raw.Pos, expr.Properties{}), l, r)
	if err != nil {
		return nil, err
	}
	if !collation.CollationConflict && (operator == "~~*" || operator == "!~~*") &&
		collation.Usage.Collation.Unicode() {
		return nil, expr.ErrorAt("0A000", "ILIKE is unsupported for this collation", raw.Pos)
	}
	provider, err := sqltype.BindLike(operator, l.ResultUsage(), r.ResultUsage(), b.Type(sqltype.Bool).Usage(), collation.CollationConflict)
	if err != nil {
		return nil, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	info, err := b.BuiltinOperatorRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, LikeProperties(l, r)), operator, lhs.ID(), rhs.ID())
	if err != nil {
		return nil, err
	}
	return plan.LikeExpr{Info: info, Left: l, Right: r, Provider: provider, Operator: operator, Namespace: namespace}, nil
}
