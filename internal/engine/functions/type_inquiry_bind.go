package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindTypeInquiry(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [2]Argument
	var names []string
	if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		intent := DirectDeferredArgument
		if raw.Name.Parts[len(raw.Name.Parts)-1].Text == "pg_typeof" {
			intent = TypeInquiryArgument
		}
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: intent})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = arg
		}
		if names != nil {
			name := "unknown"
			logicalUnknown, err := typeInquiryLogicalUnknown(arg)
			if err != nil {
				return nil, err
			}
			if !logicalUnknown && arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind, result, arity := TypeofInquiry, sqltype.Regtype, 1
	formals := [2]sqltype.ID{2276, 0}
	switch name {
	case "format_type":
		kind, result, arity = FormatTypeInquiry, sqltype.Text, 2
		formals = [2]sqltype.ID{sqltype.Oid, sqltype.Int4}
	case "pg_basetype":
		kind, formals[0] = BaseTypeInquiry, sqltype.Regtype
	}
	missing := func() error { return b.SignatureError(raw.Name, raw.Pos, names) }
	if !visible || len(raw.Args) != arity || len(raw.ArgNames) != 0 || raw.ResolutionOnly {
		return nil, missing()
	}
	n := TypeInquiry{Kind: kind, Args: make([]expr.Expr, arity)}
	properties := expr.Properties{Volatility: expr.Stable}
	for i := range n.Args {
		arg := operands[i]
		if kind == TypeofInquiry {
			if arg.TypeInquiryStringIO != nil {
				n.logical, n.stringIO, n.Args[i] = sqltype.Unknown, arg.TypeInquiryStringIO, arg.Expression
				carrier, ok := n.Args[i].(expr.InputCaster)
				if !ok {
					return nil, fmt.Errorf("stuntgres: invalid unknown input cast transport")
				}
				cast, castErr := carrier.LogicalInputCast()
				if castErr != nil {
					return nil, castErr
				}
				if cast.Target.Type != n.stringIO || cast.Target.Type.ID() != sqltype.Unknown {
					return nil, fmt.Errorf("stuntgres: invalid unknown input cast target")
				}
				n.stringIOSyntax, n.stringIOChain = cast.Syntax, cast.Transparent
			} else if witness := arg.TypeInquiryUnknown; witness != nil {
				n.logical = sqltype.Unknown
				n.Args[i] = arg.Expression
				if n.Args[i] == nil {
					n.Args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
				}
				if err == nil {
					n.unknown = &typeInquiryUnknown{raw: witness.Literal, transport: n.Args[i], castType: witness.CastType, source: witness.Raw}
				}
			} else {
				n.Args[i] = arg.Expression
				if !b.RetainUntypedParameter(arg.Expression) && arg.Usage.Type != nil {
					n.logical = arg.Usage.Type.ID()
				}
			}
		} else {
			n.Args[i], err = coerceInquiryFormal(b, arg, raw.Args[i], b.Type(formals[i]).Usage(), missing)
		}
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, n.Args[i].Properties())
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	n.Info, err = b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formals[:arity]...)
	if err != nil {
		return nil, err
	}
	n.result, n.routine = n.Usage.Type, n.RoutineReference
	if kind != TypeofInquiry {
		for i := range n.Args {
			n.formals[i] = b.Type(formals[i])
		}
	}
	if kind != TypeofInquiry {
		b.NoteVisibilitySnapshot()
	}
	return n, nil
}

// Diagnostics use the same authenticated logical identity as applicability and
// execution, never the concrete TEXT carrier or the spelling of the source.
func typeInquiryLogicalUnknown(arg Argument) (bool, error) {
	if witness := arg.TypeInquiryUnknown; witness != nil {
		if arg.TypeInquiryStringIO != nil || witness.Literal.Value.Kind != parser.StringValue && witness.Literal.Value.Kind != parser.NullValue {
			return false, fmt.Errorf("stuntgres: invalid logical unknown argument")
		}
		source := expr.UnknownInput{Literal: witness.Literal, Source: witness.Raw, CastType: witness.CastType, Chain: witness.Chain}
		if err := source.ValidateProvenance(); err != nil {
			return false, err
		}
		return true, nil
	}
	if arg.TypeInquiryStringIO != nil {
		caster, ok := arg.Expression.(expr.InputCaster)
		if !ok {
			return false, fmt.Errorf("stuntgres: missing logical unknown IO argument")
		}
		cast, err := caster.LogicalInputCast()
		if err != nil {
			return false, err
		}
		if cast.Target.Type != arg.TypeInquiryStringIO || cast.Target.Type.ID() != sqltype.Unknown {
			return false, fmt.Errorf("stuntgres: invalid logical unknown IO target")
		}
		return true, nil
	}
	return arg.Usage.Type == nil, nil
}
