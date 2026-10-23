package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindFTSQuery(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	op := sqltype.FTSQueryFunction(255)
	switch name {
	case "numnode":
		op = sqltype.FTSNumNode
	case "querytree":
		op = sqltype.FTSQueryTree
	case "ts_rewrite":
		if len(raw.Args) == 2 {
			op = sqltype.FTSRewriteSQL
		} else {
			op = sqltype.FTSRewrite
		}
	}
	var small [3]Argument
	operands := small[:0]
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	valid := op.Name() != "" && visible && len(operands) == op.Arity() && len(raw.ArgNames) == 0 && !raw.Variadic
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(op.Formal(i)).Usage(), sqltype.Implicit); err != nil {
					valid = false
				}
			}
		}
	}
	if !valid {
		names := make([]string, len(operands))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		return nil, ResolutionError(raw.Name, raw.Pos, names, false)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var args [3]expr.Expr
	var formals [3]sqltype.ID
	properties := expr.Properties{Volatility: expr.Immutable}
	if op == sqltype.FTSRewriteSQL {
		properties.Volatility = expr.Volatile
	}
	for i, arg := range operands {
		formals[i] = op.Formal(i)
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(formals[i]).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(op.ResultID()).Usage(), raw.Pos, properties), FTSQueryRoutineProvider, name, formals[:op.Arity()]...)
	if err != nil {
		return nil, err
	}
	return NewFTSQuery(info, op, args)
}
