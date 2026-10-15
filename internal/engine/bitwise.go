package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type bitwiseExpression struct {
	expressionInfo
	left, right BoundExpr
	provider    sqltype.Bitwise
	bits        sqltype.BitStringBitwise
	operator    sqltype.BitwiseOperator
	namespace   string
}

func (e bitwiseExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	a, err := e.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.right == nil {
		if e.usage.Type.ID() == sqltype.Bit {
			return e.bits.Unary(c.Context, a)
		}
		return e.provider.Unary(c.Context, a)
	}
	b, err := e.right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.usage.Type.ID() == sqltype.Bit {
		return e.bits.Binary(c.Context, a, b)
	}
	return e.provider.Binary(c.Context, a, b)
}

// Only a physical SQL constant NULL is decisive after complete analysis and
// child planning. Imported parameter values and row-owned NULLs are not planning
// facts; conservative row/stage dependence on an already-pruned literal remains.
func bitwiseConstantNull(node BoundExpr) bool {
	literal, ok := node.(literalExpression)
	return ok && !literal.properties.ParameterDependent && literal.value.IsNull()
}

func integralBitwiseType(t *sqltype.Descriptor) bool {
	return t != nil && (t.BaseType().ID() == sqltype.Int2 || t.BaseType().ID() == sqltype.Int4 || t.BaseType().ID() == sqltype.Int8)
}
func bitStringType(t *sqltype.Descriptor) bool {
	return t != nil && (t.BaseType().ID() == sqltype.Bit || t.BaseType().ID() == sqltype.VarBit)
}
func bitwiseName(name string) bool {
	for _, c := range name {
		if c == '&' || c == '|' || c == '#' || c == '~' {
			return true
		}
	}
	return name == "<<" || name == ">>"
}

func selectedBitwiseName(name string) bool {
	switch name {
	case "&", "|", "#", "~", "<<", ">>":
		return true
	}
	return false
}

func (b *expressionBinder) bindBitwise(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 || len(raw.Args) < 1 || len(raw.Args) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid bitwise expression")
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	policy := expressionPolicy{context: projectionContext}
	left, err := b.comparisonOperandPolicy(raw.Args[0], policy)
	if err != nil {
		return nil, err
	}
	var right comparisonOperand
	if len(raw.Args) == 2 {
		right, err = b.comparisonOperandPolicy(raw.Args[1], policy)
		if err != nil {
			return nil, err
		}
	}
	lt, rt := left.descriptor(), right.descriptor()
	if lt != nil {
		lt = lt.BaseType()
	}
	if rt != nil {
		rt = rt.BaseType()
	}
	missing := func() error {
		return operatorExpressionError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883"}, lt, rt, raw)
	}
	if !visible {
		return nil, missing()
	}
	ambiguous := func() error {
		typeName := func(t *sqltype.Descriptor) string {
			if t == nil {
				return "unknown"
			}
			return t.DiagnosticName()
		}
		signature := objectNameText(raw.Name) + " " + typeName(lt)
		if len(raw.Args) == 2 {
			signature = typeName(lt) + " " + objectNameText(raw.Name) + " " + typeName(rt)
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42725", Message: "operator is not unique: " + signature, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts.", Position: strconv.Itoa(raw.Pos.Character)}
	}
	var op sqltype.BitwiseOperator
	switch name {
	case "&":
		op = sqltype.BitwiseAnd
	case "|":
		op = sqltype.BitwiseOr
	case "#":
		op = sqltype.BitwiseXor
	case "~":
		op = sqltype.BitwiseNot
	case "<<":
		op = sqltype.BitwiseShiftLeft
	case ">>":
		op = sqltype.BitwiseShiftRight
	default:
		return nil, missing()
	}
	if (op == sqltype.BitwiseNot) != (len(raw.Args) == 1) {
		return nil, missing()
	}
	bitSignature := bitStringType(lt) || bitStringType(rt)
	target, rightTarget := lt, lt
	if bitSignature {
		switch op {
		case sqltype.BitwiseNot:
			if !bitStringType(lt) {
				return nil, missing()
			}
			rightTarget = nil
		case sqltype.BitwiseShiftLeft, sqltype.BitwiseShiftRight:
			if !bitStringType(lt) || rt != nil && rt.ID() != sqltype.Int2 && rt.ID() != sqltype.Int4 {
				return nil, missing()
			}
			rightTarget = b.typ(sqltype.Int4)
		default:
			if lt != nil && !bitStringType(lt) || rt != nil && !bitStringType(rt) {
				return nil, missing()
			}
			rightTarget = b.typ(sqltype.Bit)
		}
		target = b.typ(sqltype.Bit)
	} else {
		if lt != nil && !integralBitwiseType(lt) || rt != nil && !integralBitwiseType(rt) {
			return nil, missing()
		}
		if op == sqltype.BitwiseNot {
			if lt == nil {
				return nil, ambiguous()
			}
			rightTarget = nil
		} else if op == sqltype.BitwiseShiftLeft || op == sqltype.BitwiseShiftRight {
			if rt != nil && rt.ID() == sqltype.Int8 {
				return nil, missing()
			}
			if lt == nil {
				if rt == nil || rt.ID() == sqltype.Int2 {
					return nil, ambiguous()
				}
				target = rt
			}
			rightTarget = b.typ(sqltype.Int4)
		} else {
			if target == nil {
				target = rt
			}
			if target == nil {
				return nil, ambiguous()
			}
			rank := func(t *sqltype.Descriptor) int {
				if t == nil {
					return 0
				}
				switch t.ID() {
				case sqltype.Int2:
					return 1
				case sqltype.Int4:
					return 2
				default:
					return 3
				}
			}
			if rank(rt) > rank(target) {
				target = rt
			}
			rightTarget = target
		}
	}
	finish := func(o comparisonOperand, t *sqltype.Descriptor) (BoundExpr, error) {
		n, err := b.finalizeOperand(o, t)
		if err != nil {
			return nil, err
		}
		if n.ResultUsage() != t.Usage() {
			plan, err := sqltype.PlanCoercion(n.ResultUsage(), t.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			n = coercionExpression{b.info(t.Usage(), n.SourcePosition(), n.Properties()), n, plan, sqltype.Implicit, false}
		}
		return n, nil
	}
	l, err := finish(left, target)
	if err != nil {
		return nil, err
	}
	var r BoundExpr
	p := l.Properties()
	if len(raw.Args) == 2 {
		r, err = finish(right, rightTarget)
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
	var provider sqltype.Bitwise
	var bits sqltype.BitStringBitwise
	if bitSignature {
		bits, err = sqltype.BindBitStringBitwise(op, target, rightTarget)
	} else {
		provider, err = sqltype.BindBitwise(op, target, rightTarget)
	}
	if err != nil {
		return nil, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return bitwiseExpression{b.info(target.Usage(), parser.ExpressionStart(raw), p), l, r, provider, bits, op, namespace}, nil
}
