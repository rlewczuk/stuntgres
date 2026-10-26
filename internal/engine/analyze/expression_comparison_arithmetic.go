package analyze

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *Binder) BindComparison(raw parser.OperatorExpr) (expr.Expr, error) {
	return b.bindComparisonCandidates(raw, nil)
}

func (b *Binder) bindComparisonCandidates(raw parser.OperatorExpr, candidates *operatorCandidateSet) (expr.Expr, error) {
	if len(raw.Args) != 2 || len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid comparison expression")
	}
	operator, ok := ComparisonOperator(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid comparison operator")
	}
	if left, ok := raw.Args[0].(parser.RowExpr); ok {
		if right, ok := raw.Args[1].(parser.RowExpr); ok {
			return b.bindRowComparison(raw, operator, left, right)
		}
	}
	// Copy each occurrence's then-known parameter type before resolving either side.
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	right, err := b.comparisonOperand(raw.Args[1])
	if err != nil {
		return nil, err
	}
	return b.bindComparisonOperandCandidates(raw, operator, left, right, candidates)
}

func (b *Binder) BindComparisonOperands(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right ComparisonOperand) (expr.Expr, error) {
	return b.bindComparisonOperandCandidates(raw, operator, left, right, nil)
}

func (b *Binder) bindComparisonOperandCandidates(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right ComparisonOperand, candidates *operatorCandidateSet) (expr.Expr, error) {
	lt, rt, err := b.resolveComparisonCandidates(raw, left.descriptor(), right.descriptor(), candidates)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	declaration, _, err := b.comparisonRoutineDeclaration(name, lt, rt)
	if err != nil {
		return nil, err
	}
	// VARCHAR has binary-compatible TEXT operator formals, but a contextual
	// literal still owns its original concrete usage for stored CHECK replay.
	// Keep that source node and put the conversion on this occurrence.
	contextualize := func(operand ComparisonOperand, source, target *sqltype.Descriptor) (ComparisonOperand, error) {
		if operand.Node == nil && source != nil && source.BaseType().ID() == sqltype.VarChar && target.ID() == sqltype.Text {
			node, err := b.finalizeOperand(operand, source.BaseType())
			operand.Node = node
			return operand, err
		}
		return operand, nil
	}
	left, err = contextualize(left, right.descriptor(), lt)
	if err != nil {
		return nil, err
	}
	right, err = contextualize(right, left.descriptor(), rt)
	if err != nil {
		return nil, err
	}
	l, err := b.CoerceConditional(left, lt.Usage(), name)
	if err != nil {
		return nil, err
	}
	r, err := b.CoerceConditional(right, rt.Usage(), name)
	if err != nil {
		return nil, err
	}
	input, err := b.comparisonInfo(l, r)
	if err != nil {
		return nil, err
	}
	predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, r.ResultUsage().Type, b.Type(sqltype.Bool), input.Usage.Collation, input.CollationConflict)
	if err != nil {
		return nil, functions.ComparisonError(b, err, left.descriptor(), right.descriptor(), raw)
	}
	p, rp := l.Properties(), r.Properties()
	p.RowDependent = p.RowDependent || rp.RowDependent
	p.ParameterDependent = p.ParameterDependent || rp.ParameterDependent
	p.StageDependent = p.StageDependent || rp.StageDependent
	p.DomainDependent = p.DomainDependent || rp.DomainDependent
	p.RuntimeDomainConversion = p.RuntimeDomainConversion || rp.RuntimeDomainConversion
	if rp.Volatility > p.Volatility {
		p.Volatility = rp.Volatility
	}
	if predicate.SessionDependent() && p.Volatility < expr.Stable {
		p.Volatility = expr.Stable
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	info := b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, p)
	var routineRef ids.ObjectRef
	if declaration != nil {
		info, routineRef, err = b.selectedComparisonRoutineInfo(info, declaration)
		if err != nil {
			return nil, err
		}
	}
	return plan.ComparisonExpr{Info: info, RoutineRef: routineRef, Left: l, Right: r, Predicate: predicate, Operator: name, Namespace: namespace}, nil
}

func arithmeticName(name string) bool {
	return name == "+" || name == "-" || name == "*" || name == "/" || name == "%" || name == "^"
}
func symbolicArithmeticName(name string) bool {
	if len(name) < 2 {
		return false
	}
	for _, c := range name {
		if c != '+' && c != '-' && c != '*' && c != '/' && c != '%' && c != '^' {
			return false
		}
	}
	return true
}
func (b *Binder) BindArithmetic(raw parser.OperatorExpr) (expr.Expr, error) {
	if len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 || len(raw.Args) < 1 || len(raw.Args) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid arithmetic expression")
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	// Only syntactic signed constants normalize. Other unary expressions keep
	// their provider tree and runtime overflow/error phase.
	if (name == "+" || name == "-") && len(raw.Args) == 1 && len(raw.Name.Parts) == 1 {
		if literal, ok := raw.Args[0].(parser.LiteralExpr); ok {
			switch literal.Value.Kind {
			case parser.IntegerValue:
				if name == "-" && literal.Value.Integer != -1<<63 {
					literal.Value.Integer = -literal.Value.Integer
				} else if name == "-" {
					break
				}
				literal.Value.Pos = raw.Pos
				return b.Bind(literal, ExpressionPolicy{Context: ProjectionContext})
			case parser.NumericValue:
				if name == "-" {
					if strings.HasPrefix(literal.Value.Text, "-") {
						literal.Value.Text = strings.TrimPrefix(literal.Value.Text, "-")
					} else {
						literal.Value.Text = "-" + literal.Value.Text
					}
				}
				return b.Bind(literal, ExpressionPolicy{Context: ProjectionContext})
			}
		}
	}
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	var right ComparisonOperand
	if len(raw.Args) == 2 {
		right, err = b.comparisonOperand(raw.Args[1])
		if err != nil {
			return nil, err
		}
	}
	return b.finishArithmetic(raw, left, right)
}

func (b *Binder) finishArithmetic(raw parser.OperatorExpr, left, right ComparisonOperand) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operator := arithmeticOperator(name)
	lt, rt := left.descriptor(), right.descriptor()
	if lt != nil {
		lt = lt.BaseType()
	}
	if rt != nil {
		rt = rt.BaseType()
	}
	visible, err := b.builtinVisible(raw.Name, OperatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), lt, rt, raw)
	}
	if name == "-" && len(raw.Args) == 2 {
		if node, selected, err := functions.SelectJSONBDelete(b, raw, operandArgument(left), operandArgument(right)); selected {
			return node, err
		}
	}
	if symbolicArithmeticName(name) {
		return nil, functions.OperatorError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883"}, lt, rt, raw)
	}
	signature, err := sqltype.ResolveArithmetic(operator, lt, rt, len(raw.Args) == 1)
	if err != nil {
		return nil, functions.OperatorError(err, lt, rt, raw)
	}
	typ := b.Type(signature.Result)
	leftType := b.Type(signature.Left)
	var rightType *sqltype.Descriptor
	if len(raw.Args) == 2 {
		rightType = b.Type(signature.Right)
	}
	provider, err := sqltype.BindArithmetic(operator, leftType, rightType, typ)
	if err != nil {
		return nil, err
	}
	finish := func(o ComparisonOperand, target *sqltype.Descriptor) (expr.Expr, error) {
		source := o.descriptor()
		if source == nil {
			source = target
		}
		n, err := b.finalizeOperand(o, source)
		if err != nil {
			return nil, err
		}
		if source != target {
			if selected, handled, selectionErr := b.declaredCoercion(n, target.Usage(), sqltype.Implicit, n.SourcePosition(), o.raw); handled {
				return selected, selectionErr
			}
			coercionPlan, err := sqltype.PlanCoercion(n.ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			info, err := b.coercionRoutineInfo(b.Info(target.Usage(), n.SourcePosition(), CoercionProperties(n, coercionPlan)), n)
			if err != nil {
				return nil, err
			}
			n = plan.CoercionExpr{Info: info, Child: n, Plan: coercionPlan, Mode: sqltype.Implicit}
		}
		return n, nil
	}
	l, err := finish(left, leftType)
	if err != nil {
		return nil, err
	}
	var r expr.Expr
	p := l.Properties()
	if len(raw.Args) == 2 {
		r, err = finish(right, rightType)
		if err != nil {
			return nil, err
		}
		rp := r.Properties()
		if rp.Volatility > p.Volatility {
			p.Volatility = rp.Volatility
		}
		p.RowDependent = p.RowDependent || rp.RowDependent
		p.ParameterDependent = p.ParameterDependent || rp.ParameterDependent
		p.StageDependent = p.StageDependent || rp.StageDependent
		p.DomainDependent = p.DomainDependent || rp.DomainDependent
		p.RuntimeDomainConversion = p.RuntimeDomainConversion || rp.RuntimeDomainConversion
	}
	if provider.SessionDependent() && p.Volatility < expr.Stable {
		p.Volatility = expr.Stable
	}
	// Contextual consumers use the cached expression start; signature errors
	// above retain the raw operator token.
	pos := raw.Pos
	for _, child := range [...]expr.Expr{l, r} {
		if child != nil {
			start := child.SourcePosition()
			if start.Character > 0 && (pos.Character <= 0 || start.Character < pos.Character) {
				pos = start
			}
		}
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	formalLeft, formalRight := signature.Left, signature.Right
	if len(raw.Args) == 1 {
		formalLeft, formalRight = 0, signature.Left
	}
	info, err := b.builtinOperatorRoutineInfo(b.Info(typ.Usage(), pos, p), name, formalLeft, formalRight)
	if err != nil {
		return nil, err
	}
	return plan.ArithmeticExpr{Info: info, Left: l, Right: r, Provider: provider, Operator: operator, Namespace: namespace}, nil
}
