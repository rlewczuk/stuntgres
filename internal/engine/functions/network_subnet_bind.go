package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The original INTERNAL routines share the symbolic operators' typed provider
// and routine authority. Inventory entries alone do not admit function calls.
func bindNetworkSubnet(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [2]Argument
	var operands []Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = arg
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	valid := visible && len(operands) == 2 && len(raw.ArgNames) == 0 && !raw.Variadic
	if valid {
		for _, arg := range operands {
			if arg.Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(sqltype.Inet).Usage(), sqltype.Implicit); err != nil {
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
		return nil, UnnotedResolutionError(b, raw.Name, raw.Pos, names, false)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var operation sqltype.NetworkSubnetOperator
	switch name {
	case "network_sub":
		operation = sqltype.NetworkSubnetSub
	case "network_subeq":
		operation = sqltype.NetworkSubnetSubeq
	case "network_sup":
		operation = sqltype.NetworkSubnetSup
	case "network_supeq":
		operation = sqltype.NetworkSubnetSupeq
	case "network_overlap":
		operation = sqltype.NetworkSubnetOverlap
	}
	var args [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, arg := range operands {
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(sqltype.Inet).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), OperatorRoutineProvider, name, sqltype.Inet, sqltype.Inet)
	if err != nil {
		return nil, err
	}
	return NewNetworkSubnet(info, operation, args, "pg_catalog")
}
