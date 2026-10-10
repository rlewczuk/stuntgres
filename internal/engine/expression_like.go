package engine

import (
	"fmt"

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

type likeEscapeExpression struct {
	expressionInfo
	pattern, escape BoundExpr
	provider        sqltype.LikeEscapePlan
}

func (e likeEscapeExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if randomLiteralNull(e.pattern) || randomLiteralNull(e.escape) {
		return sqltype.Null(e.usage.Type), nil
	}
	pattern, err := e.pattern.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	escape, err := e.escape.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.provider.Evaluate(c.Context, pattern, escape)
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
	info, err := b.collatedInfo(b.info(u, node.SourcePosition(), coercionProperties(node, plan)), node)
	if err != nil {
		return nil, err
	}
	return coercionExpression{info, node, plan, sqltype.Implicit}, nil
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
	return likeExpression{b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, likeProperties(l, r)), l, r, provider, operator, namespace}, nil
}

func (b *expressionBinder) bindLikeEscape(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	if len(raw.Args) != 2 {
		return nil, b.functionSignatureError(raw)
	}
	var operands [2]comparisonOperand
	var names []string
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		operands[i] = o
		name := "unknown"
		if o.descriptor() != nil {
			name = o.descriptor().DiagnosticName()
		}
		names = append(names, name)
	}
	missing := func() (BoundExpr, error) { return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	if !visible || raw.Variadic {
		return missing()
	}
	target := b.typ(sqltype.Text)
	for _, o := range operands {
		if d := o.descriptor(); d != nil && d.BaseType().ID() == sqltype.Bytea {
			target = b.typ(sqltype.Bytea)
		}
	}
	for _, o := range operands {
		if d := o.descriptor(); d != nil {
			if target.ID() == sqltype.Bytea && d.BaseType().ID() != sqltype.Bytea {
				return missing()
			}
			if target.ID() == sqltype.Text && d.BaseType().ID() != sqltype.Text && d.BaseType().ID() != sqltype.VarChar && d.BaseType().ID() != sqltype.BPChar && d.BaseType().ID() != sqltype.Name {
				return missing()
			}
		}
	}
	pattern, err := b.likeOperand(operands[0], target)
	if err != nil {
		return nil, err
	}
	escape, err := b.likeOperand(operands[1], target)
	if err != nil {
		return nil, err
	}
	u := target.Usage()
	info, err := b.collatedInfo(b.info(u, raw.Pos, likeProperties(pattern, escape)), pattern, escape)
	if err != nil {
		return nil, err
	}
	u = info.usage
	provider, err := sqltype.BindLikeEscape(pattern.ResultUsage(), escape.ResultUsage(), u)
	if err != nil {
		return nil, err
	}
	return likeEscapeExpression{info, pattern, escape, provider}, nil
}
