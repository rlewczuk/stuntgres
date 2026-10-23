package analyze

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

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

func (b *Binder) BindBitwise(raw parser.OperatorExpr) (expr.Expr, error) {
	if len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 || len(raw.Args) < 1 || len(raw.Args) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid bitwise expression")
	}
	visible, err := b.builtinVisible(raw.Name, OperatorLookup)
	if err != nil {
		return nil, err
	}
	policy := ExpressionPolicy{Context: ProjectionContext}
	left, err := b.comparisonOperandPolicy(raw.Args[0], policy)
	if err != nil {
		return nil, err
	}
	var right ComparisonOperand
	if len(raw.Args) == 2 {
		right, err = b.comparisonOperandPolicy(raw.Args[1], policy)
		if err != nil {
			return nil, err
		}
	}
	return b.finishBitwise(raw, left, right, visible)
}

func (b *Binder) finishBitwise(raw parser.OperatorExpr, left, right ComparisonOperand, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	lt, rt := left.descriptor(), right.descriptor()
	if lt != nil {
		lt = lt.BaseType()
	}
	if rt != nil {
		rt = rt.BaseType()
	}
	missing := func() error {
		return functions.OperatorError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883"}, lt, rt, raw)
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
		signature := catalog.ObjectNameText(raw.Name) + " " + typeName(lt)
		if len(raw.Args) == 2 {
			signature = typeName(lt) + " " + catalog.ObjectNameText(raw.Name) + " " + typeName(rt)
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
	networkSignature := lt != nil && sqltype.IsNetwork(lt.ID()) || rt != nil && sqltype.IsNetwork(rt.ID())
	target, rightTarget := lt, lt
	if networkSignature {
		if op != sqltype.BitwiseNot && op != sqltype.BitwiseAnd && op != sqltype.BitwiseOr ||
			lt != nil && !sqltype.IsNetwork(lt.ID()) || rt != nil && !sqltype.IsNetwork(rt.ID()) {
			return nil, missing()
		}
		target, rightTarget = b.Type(sqltype.Inet), b.Type(sqltype.Inet)
		if op == sqltype.BitwiseNot {
			rightTarget = nil
		}
	} else if bitSignature {
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
			rightTarget = b.Type(sqltype.Int4)
		default:
			if lt != nil && !bitStringType(lt) || rt != nil && !bitStringType(rt) {
				return nil, missing()
			}
			rightTarget = b.Type(sqltype.Bit)
		}
		target = b.Type(sqltype.Bit)
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
			rightTarget = b.Type(sqltype.Int4)
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
	finish := func(o ComparisonOperand, t *sqltype.Descriptor) (expr.Expr, error) {
		n, err := b.finalizeOperand(o, t)
		if err != nil {
			return nil, err
		}
		if n.ResultUsage() != t.Usage() {
			if selected, handled, selectionErr := b.declaredCoercion(n, t.Usage(), sqltype.Implicit, n.SourcePosition(), o.raw); handled {
				return selected, selectionErr
			}
			coercionPlan, err := sqltype.PlanCoercion(n.ResultUsage(), t.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			n = plan.CoercionExpr{Info: b.Info(t.Usage(), n.SourcePosition(), n.Properties()), Child: n, Plan: coercionPlan, Mode: sqltype.Implicit}
		}
		return n, nil
	}
	l, err := finish(left, target)
	if err != nil {
		return nil, err
	}
	var r expr.Expr
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
	var network sqltype.NetworkBitwise
	if networkSignature {
		network, err = sqltype.BindNetworkBitwise(op, target, rightTarget)
	} else if bitSignature {
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
	formalLeft, formalRight := target.ID(), sqltype.ID(0)
	if op == sqltype.BitwiseNot {
		formalLeft, formalRight = 0, target.ID()
	} else {
		formalRight = rightTarget.ID()
	}
	selectedInfo, err := b.builtinOperatorRoutineInfo(b.Info(target.Usage(), parser.ExpressionStart(raw), p), name, formalLeft, formalRight)
	if err != nil {
		return nil, err
	}
	return plan.BitwiseExpr{
		Info: selectedInfo,
		Left: l, Right: r, Provider: provider, Bits: bits, Network: network,
		Operator: op, Namespace: namespace,
	}, nil
}
