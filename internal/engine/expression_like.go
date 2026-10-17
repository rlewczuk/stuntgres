package engine

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type likeExpression struct {
	expressionInfo
	left, right         BoundExpr
	provider            sqltype.LikePlan
	operator, namespace string
}

func (e likeExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	a, err := e.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.provider.Evaluate(c.Context, a, b)
}

func likeName(name string) bool {
	return name == "~~" || name == "!~~" || name == "~~*" || name == "!~~*"
}

func likeProperties(a, b BoundExpr) ExprProperties {
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

func (b *expressionBinder) collatedInfo(info expressionInfo, inputs ...BoundExpr) (expressionInfo, error) {
	return collatedInfo(b.bindingCatalog(), info, inputs...)
}

// Coercion inherits its child's authority except CoerceToDomain to a declared
// nondefault collation. Targeting coercions move top-level COLLATE wrappers
// outside their domain boundary; nested explicit authority is replaced here.
func coercionInfo(view CatalogRead, info expressionInfo, child BoundExpr) (expressionInfo, error) {
	info.collationExplicit = parser.Position{}
	info.collationConflict = false
	info.collationLeft, info.collationRight, info.collationConflictPos = sqltype.CollationRef{}, sqltype.CollationRef{}, parser.Position{}
	if info.usage.Collation.ID == 0 {
		return info, nil
	}
	if info.usage.Type.IsDomain() && child.ResultUsage().Type != info.usage.Type {
		declared := info.usage.Type.Usage().Collation
		if declared.ID != 0 && declared.ID != 100 {
			info.usage.Collation = declared
			return info, nil
		}
	}
	if source := child.ResultUsage().Collation; source.ID != 0 {
		info.usage.Collation = source
	}
	return collatedInfo(view, info, child)
}

func (b *expressionBinder) coercionInfo(info expressionInfo, child BoundExpr) (expressionInfo, error) {
	info, err := coercionInfo(b.bindingCatalog(), info, child)
	if err != nil {
		return expressionInfo{}, err
	}
	return b.coercionRoutineInfo(info, child)
}

// Some successful coercion seams deliberately retain their own collation
// handling. They still select the same actual function-method identity.
func (b *expressionBinder) coercionRoutineInfo(info expressionInfo, child BoundExpr) (expressionInfo, error) {
	if id := embeddedCastOID(child.ResultUsage().Type, info.usage.Type); id != 0 {
		return b.selectedBuiltinRoutineInfo(info, builtinRoutineByOID[id])
	}
	return info, nil
}

func comparisonInfo(view CatalogRead, left, right BoundExpr) (expressionInfo, error) {
	usage := left.ResultUsage()
	if usage.Collation.ID == 0 {
		usage = right.ResultUsage()
	}
	return collatedInfo(view, expressionInfo{usage: usage}, left, right)
}

func (b *expressionBinder) comparisonInfo(left, right BoundExpr) (expressionInfo, error) {
	if err := b.captureComparisonDiagnosticInput(left.ResultUsage().Type); err != nil {
		return expressionInfo{}, err
	}
	if err := b.captureComparisonDiagnosticInput(right.ResultUsage().Type); err != nil {
		return expressionInfo{}, err
	}
	return comparisonInfo(b.bindingCatalog(), left, right)
}

// Collation identity is valid Usage metadata; indeterminacy is expression
// state. Preserve both across collatable wrappers without inventing an invalid
// stored collation or evaluating branches merely to choose their collation.
func collatedInfo(view CatalogRead, info expressionInfo, inputs ...BoundExpr) (expressionInfo, error) {
	if info.usage.Collation.ID == 0 {
		return info, nil
	}
	for _, input := range inputs {
		u, explicit := input.ResultUsage(), input.ExplicitCollation()
		if u.Collation.ID == 0 {
			continue
		}
		if explicit.Character > 0 {
			if info.collationExplicit.Character > 0 && info.usage.Collation.ID != u.Collation.ID {
				return info, expressionError("42P21", fmt.Sprintf(`collation mismatch between explicit collations "%s" and "%s"`, collationName(view, info.usage.Collation.ID), collationName(view, u.Collation.ID)), explicit)
			}
			info.usage.Collation = u.Collation
			if info.collationExplicit.Character == 0 {
				info.collationExplicit = explicit
			}
			info.collationConflict = false
			info.collationLeft, info.collationRight, info.collationConflictPos = sqltype.CollationRef{}, sqltype.CollationRef{}, parser.Position{}
		} else if info.collationExplicit.Character == 0 {
			selected, conflict := likeCollation(info.usage, u)
			indeterminate := input.CollationIndeterminate()
			if !info.collationConflict {
				if indeterminate {
					info.collationLeft, info.collationRight, info.collationConflictPos = input.ImplicitCollationConflict()
				} else if conflict {
					info.collationLeft, info.collationRight, info.collationConflictPos = info.usage.Collation, u.Collation, input.SourcePosition()
				}
			}
			info.usage.Collation = selected
			info.collationConflict = info.collationConflict || conflict || indeterminate
		}
	}
	return info, nil
}

func (b *expressionBinder) likeOperand(o comparisonOperand, target *sqltype.Descriptor) (BoundExpr, error) {
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
	info, err := b.coercionInfo(b.info(u, node.SourcePosition(), coercionProperties(node, plan)), node)
	if err != nil {
		return nil, err
	}
	return coercionExpression{info, node, plan, sqltype.Implicit, false}, nil
}

func (b *expressionBinder) bindLike(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Args) != 2 || len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid LIKE expression")
	}
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	right, err := b.comparisonOperand(raw.Args[1])
	if err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	missing := func() (BoundExpr, error) {
		return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), left.descriptor(), right.descriptor(), raw)
	}
	if !visible {
		return missing()
	}
	operator := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	lt, rt := left.descriptor(), right.descriptor()
	if lt != nil {
		lt = lt.BaseType()
	}
	if rt != nil {
		rt = rt.BaseType()
	}
	lhs, rhs := b.typ(sqltype.Text), b.typ(sqltype.Text)
	if lt != nil && lt.ID() == sqltype.Bytea || lt == nil && rt != nil && rt.ID() == sqltype.Bytea {
		if operator == "~~*" || operator == "!~~*" {
			return missing()
		}
		lhs, rhs = b.typ(sqltype.Bytea), b.typ(sqltype.Bytea)
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
	l, err := b.likeOperand(left, lhs)
	if err != nil {
		return nil, err
	}
	r, err := b.likeOperand(right, rhs)
	if err != nil {
		return nil, err
	}
	collation, err := b.collatedInfo(b.info(l.ResultUsage(), raw.Pos, ExprProperties{}), l, r)
	if err != nil {
		return nil, err
	}
	if !collation.collationConflict && (operator == "~~*" || operator == "!~~*") &&
		collation.usage.Collation.Unicode() {
		return nil, expressionError("0A000", "ILIKE is unsupported for this collation", raw.Pos)
	}
	provider, err := sqltype.BindLike(operator, l.ResultUsage(), r.ResultUsage(), b.typ(sqltype.Bool).Usage(), collation.collationConflict)
	if err != nil {
		return nil, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	info, err := b.builtinOperatorRoutineInfo(b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, likeProperties(l, r)), operator, lhs.ID(), rhs.ID())
	if err != nil {
		return nil, err
	}
	return likeExpression{info, l, r, provider, operator, namespace}, nil
}

func (b *expressionBinder) bindLikeEscape(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	if len(raw.Args) != 2 {
		return nil, b.functionSignatureError(raw)
	}
	var operands [2]functions.Argument[BoundExpr]
	var names []string
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = o
		name := "unknown"
		if o.Usage.Type != nil {
			name = o.Usage.Type.DiagnosticName()
		}
		names = append(names, name)
	}
	missing := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	targetID := functions.LikeEscapeTarget(operands)
	if !functions.LikeEscapeAllowed(visible, raw.Variadic, operands, targetID) {
		return missing()
	}
	target := b.typ(targetID)
	pattern, escape, err := functions.CoerceLikeEscapeArguments(b, operands, target.Usage())
	if err != nil {
		return nil, err
	}
	u := target.Usage()
	info, err := b.collatedInfo(b.info(u, raw.Pos, likeProperties(pattern, escape)), pattern, escape)
	if err != nil {
		return nil, err
	}
	info, err = b.builtinRoutineInfo(info, stringRoutineProvider, "like_escape", targetID, targetID)
	if err != nil {
		return nil, err
	}
	return functions.NewLikeEscape[BoundExpr, *EvalContext](functionInfo(info), pattern, escape)
}
