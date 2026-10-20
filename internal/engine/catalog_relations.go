package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindExtensionUpdatePaths(raw parser.FunctionExpr) (expr.Expr, error) {
	// As with other OID inquiries, transform operands before overload/decorator
	// diagnostics, but retain UNKNOWN until the sole formal type is selected.
	var operand comparisonOperand
	var single [1]string
	names := single[:0]
	if len(raw.Args) == 1 {
		names = single[:]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if len(raw.Args) == 1 {
			operand = arg
		}
		if len(raw.Args) <= 100 {
			name := "unknown"
			if typ := arg.descriptor(); typ != nil {
				name = b.diagnosticTypeName(typ)
			}
			names[i] = functions.NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if !visible || name != "pg_extension_update_paths" || len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	var arg expr.Expr
	target := b.typ(sqltype.Name).Usage()
	arg, err = b.finalizeOperand(operand, target.Type)
	if err != nil {
		return nil, err
	}
	if arg.ResultUsage() != target {
		coercionPlan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
		if failure != nil {
			if diagnostic, ok := failure.(*sqlstate.Error); ok && (diagnostic.Code == "42804" || diagnostic.Code == "42846") {
				return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
			}
			return nil, failure
		}
		arg = coercionExpression{b.info(target, raw.Args[0].SourcePosition(), coercionProperties(arg, coercionPlan)), arg, coercionPlan, sqltype.Implicit, false}
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	if err := b.stringSetAllowed(raw.Pos); err != nil {
		return nil, err
	}
	return b.bindExtensionPaths(raw, arg)
}
