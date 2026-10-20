package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type likeExpression struct {
	expr.Info
	left, right         expr.Expr
	provider            sqltype.LikePlan
	operator, namespace string
}

func (e likeExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.provider.Evaluate(ctx.FunctionContext(), a, b)
}

func likeName(name string) bool {
	return name == "~~" || name == "!~~" || name == "~~*" || name == "!~~*"
}

func likeProperties(a, b expr.Expr) expr.Properties {
	p, q := a.Properties(), b.Properties()
	p.Volatility = max(p.Volatility, q.Volatility)
	p.RowDependent = p.RowDependent || q.RowDependent
	p.ParameterDependent = p.ParameterDependent || q.ParameterDependent
	p.StageDependent = p.StageDependent || q.StageDependent
	p.DomainDependent = p.DomainDependent || q.DomainDependent
	p.RuntimeDomainConversion = p.RuntimeDomainConversion || q.RuntimeDomainConversion
	return p
}

// Implicit default yields to nondefault identities; differing nondefault
// identities remain indeterminate until an explicit annotation supplies authority.
func likeCollation(a, b sqltype.Usage) (sqltype.CollationRef, bool) {
	x, y := a.Collation, b.Collation
	if x.ID == 0 {
		return y, false
	}
	if y.ID == 0 || x.ID == y.ID {
		return x, false
	}
	if x.ID == 100 {
		return y, false
	}
	if y.ID == 100 {
		return x, false
	}
	return sqltype.CollationRef{ID: 100, Derivation: sqltype.Default}, true
}

func (b *expressionBinder) collatedInfo(info expr.Info, inputs ...expr.Expr) (expr.Info, error) {
	return collatedInfo(b.bindingCatalog(), info, inputs...)
}

// Coercion inherits its child's authority except CoerceToDomain to a declared
// nondefault collation. Targeting coercions move top-level COLLATE wrappers
// outside their domain boundary; nested explicit authority is replaced here.
func coercionInfo(view catalog.Read, info expr.Info, child expr.Expr) (expr.Info, error) {
	info.CollationExplicit = parser.Position{}
	info.CollationConflict = false
	info.CollationLeft, info.CollationRight, info.CollationConflictPos = sqltype.CollationRef{}, sqltype.CollationRef{}, parser.Position{}
	if info.Usage.Collation.ID == 0 {
		return info, nil
	}
	if info.Usage.Type.IsDomain() && child.ResultUsage().Type != info.Usage.Type {
		declared := info.Usage.Type.Usage().Collation
		if declared.ID != 0 && declared.ID != 100 {
			info.Usage.Collation = declared
			return info, nil
		}
	}
	if source := child.ResultUsage().Collation; source.ID != 0 {
		info.Usage.Collation = source
	}
	return collatedInfo(view, info, child)
}

func (b *expressionBinder) coercionInfo(info expr.Info, child expr.Expr) (expr.Info, error) {
	info, err := coercionInfo(b.bindingCatalog(), info, child)
	if err != nil {
		return expr.Info{}, err
	}
	return b.coercionRoutineInfo(info, child)
}

// Some successful coercion seams deliberately retain their own collation
// handling. They still select the same actual function-method identity.
func (b *expressionBinder) coercionRoutineInfo(info expr.Info, child expr.Expr) (expr.Info, error) {
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
	return collatedInfo(view, expr.Info{Usage: usage}, left, right)
}

func (b *expressionBinder) comparisonInfo(left, right expr.Expr) (expr.Info, error) {
	if err := b.captureComparisonDiagnosticInput(left.ResultUsage().Type); err != nil {
		return expr.Info{}, err
	}
	if err := b.captureComparisonDiagnosticInput(right.ResultUsage().Type); err != nil {
		return expr.Info{}, err
	}
	return comparisonInfo(b.bindingCatalog(), left, right)
}

// Collation identity is valid Usage metadata; indeterminacy is expression
// state. Preserve both across collatable wrappers without inventing an invalid
// stored collation or evaluating branches merely to choose their collation.
func collatedInfo(view catalog.Read, info expr.Info, inputs ...expr.Expr) (expr.Info, error) {
	if info.Usage.Collation.ID == 0 {
		return info, nil
	}
	for _, input := range inputs {
		u, explicit := input.ResultUsage(), input.ExplicitCollation()
		if u.Collation.ID == 0 {
			continue
		}
		if explicit.Character > 0 {
			if info.CollationExplicit.Character > 0 && info.Usage.Collation.ID != u.Collation.ID {
				return info, expr.ErrorAt("42P21", fmt.Sprintf(`collation mismatch between explicit collations "%s" and "%s"`, typedef.CollationName(view, info.Usage.Collation.ID), typedef.CollationName(view, u.Collation.ID)), explicit)
			}
			info.Usage.Collation = u.Collation
			if info.CollationExplicit.Character == 0 {
				info.CollationExplicit = explicit
			}
			info.CollationConflict = false
			info.CollationLeft, info.CollationRight, info.CollationConflictPos = sqltype.CollationRef{}, sqltype.CollationRef{}, parser.Position{}
		} else if info.CollationExplicit.Character == 0 {
			selected, conflict := likeCollation(info.Usage, u)
			indeterminate := input.CollationIndeterminate()
			if !info.CollationConflict {
				if indeterminate {
					info.CollationLeft, info.CollationRight, info.CollationConflictPos = input.ImplicitCollationConflict()
				} else if conflict {
					info.CollationLeft, info.CollationRight, info.CollationConflictPos = info.Usage.Collation, u.Collation, input.SourcePosition()
				}
			}
			info.Usage.Collation = selected
			info.CollationConflict = info.CollationConflict || conflict || indeterminate
		}
	}
	return info, nil
}

func (b *expressionBinder) likeOperand(o comparisonOperand, target *sqltype.Descriptor) (expr.Expr, error) {
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
	plan, err := sqltype.PlanCoercion(node.ResultUsage(), u, sqltype.Implicit)
	if err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.Info(u, node.SourcePosition(), coercionProperties(node, plan)), node)
	if err != nil {
		return nil, err
	}
	return coercionExpression{info, node, plan, sqltype.Implicit, false}, nil
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
	info, err := b.BuiltinOperatorRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, likeProperties(l, r)), operator, lhs.ID(), rhs.ID())
	if err != nil {
		return nil, err
	}
	return likeExpression{info, l, r, provider, operator, namespace}, nil
}
