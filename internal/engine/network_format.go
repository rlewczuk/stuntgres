package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindNetworkFormat(raw parser.FunctionExpr) (expr.Expr, error) {
	var small [2]functions.Argument
	var operands []functions.Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = arg
		}
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := sqltype.NetworkFormatFunction(name)
	valid := visible && kind.Arity() != 0 && len(operands) == kind.Arity() && len(raw.ArgNames) == 0 && !raw.Variadic
	if valid {
		for i, arg := range operands {
			if kind == sqltype.NetworkSetMasklen && i == 1 {
				if arg.Usage.Type != nil {
					if _, err := sqltype.PlanCoercion(arg.Usage, b.typ(sqltype.Int4).Usage(), sqltype.Implicit); err != nil {
						valid = false
					}
				}
			} else if arg.Usage.Type != nil && !sqltype.IsNetwork(arg.Usage.Type.BaseType().ID()) {
				valid = false
			}
		}
	}
	if !valid {
		names := make([]string, len(operands))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.diagnosticTypeName(arg.Usage.Type)
			}
			names[i] = functions.NamedArgumentType(raw, i, names[i])
		}
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, false)
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	formal := sqltype.Inet
	if (kind == sqltype.NetworkAbbrev || kind == sqltype.NetworkSetMasklen) && operands[0].Usage.Type != nil && operands[0].Usage.Type.BaseType() == b.typ(sqltype.Cidr) {
		formal = sqltype.Cidr
	}
	var args [2]expr.Expr
	var formals [2]sqltype.ID
	properties := expr.IntrinsicProperties(functions.NetworkFormatIntrinsicRule())
	for i, arg := range operands {
		argumentType := formal
		if kind == sqltype.NetworkSetMasklen && i == 1 {
			argumentType = sqltype.Int4
		}
		args[i], err = b.CoerceArgument(arg, functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: b.typ(argumentType).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		formals[i] = argumentType
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.builtinRoutineInfo(b.info(b.typ(kind.ResultFor(formal)).Usage(), raw.Pos, properties), functions.NetworkRoutineProvider, name, formals[:kind.Arity()]...)
	if err != nil {
		return nil, err
	}
	return functions.NewNetworkFormat(info, kind, args)
}
