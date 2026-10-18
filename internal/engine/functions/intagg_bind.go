package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// AnalyzeIntaggArguments analyzes intagg call operands, then checks the
// argument limit and labels.
func AnalyzeIntaggArguments(b Binder, raw parser.FunctionExpr) ([]Argument, error) {
	var small [1]Argument
	var operands []Argument
	if len(raw.Args) <= 1 {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	return operands, nil
}

// ResolveIntagg selects the installed intagg routine for one operand:
// int_array_enum(integer[]) or int_array_aggregate(integer). Search path
// errors precede signature errors; the operand is coerced to the formal.
func ResolveIntagg(b Binder, raw parser.FunctionExpr, operands []Argument) (expr.Expr, InstalledRoutine, error) {
	names := make([]string, len(operands))
	for i, o := range operands {
		names[i] = "unknown"
		if o.Usage.Type != nil {
			names[i] = b.DiagnosticTypeName(o.Usage.Type)
		}
		names[i] = NamedArgumentType(raw, i, names[i])
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	formal, signature := b.Type(sqltype.Int4).Usage(), "23"
	if strings.EqualFold(name, "int_array_enum") {
		formal, signature = sqltype.Usage{}, "1007"
	}
	routine, err := b.InstalledRoutine(raw.Name, IntaggExtension, signature)
	if err != nil {
		return nil, nil, err
	}
	if formal.Type == nil {
		array, ok := b.ArrayType(b.Type(sqltype.Int4))
		if !ok {
			return nil, nil, fmt.Errorf("stuntgres: missing integer array descriptor")
		}
		formal = array.Usage()
	}
	if raw.Star || len(operands) != 1 || len(raw.ArgNames) != 0 {
		return nil, nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if operands[0].Usage.Type != nil {
		if _, err := sqltype.PlanCoercion(operands[0].Usage, formal, sqltype.Implicit); err != nil {
			return nil, nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
	}
	if routine == nil {
		return nil, nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := routine.Validate(); err != nil {
		return nil, nil, err
	}
	arg, err := b.CoerceArgument(operands[0], CoercionRequest{Intent: DirectFormalArgument, Target: formal})
	if err != nil {
		return nil, nil, err
	}
	return arg, routine, nil
}

// bindIntagg binds intagg's installed routines called as  Only
// int_array_enum is one; int_array_aggregate called this way is missing.
func bindIntagg(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands, err := AnalyzeIntaggArguments(b, raw)
	if err != nil {
		return nil, err
	}
	arg, routine, err := ResolveIntagg(b, raw, operands)
	if err != nil {
		return nil, err
	}
	// The installed routine's name is the written name, found exactly.
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != "int_array_enum" {
		return nil, b.SignatureError(raw.Name, raw.Pos, []string{b.DiagnosticTypeName(arg.ResultUsage().Type)})
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Immutable, StageDependent: true}, arg.Properties())
	info := b.Info(b.Type(sqltype.Int4).Usage(), raw.Pos, properties)
	return routine.Attach(IntaggEnumeration{Info: info, Arg: arg})
}

// IntaggEnumeration is intagg's bound int_array_enum call before the engine
// attaches its installed procedure. Attach applies set-returning admission
// and returns the engine's set-returning node, so this node is never
// evaluated.
type IntaggEnumeration struct {
	expr.Info
	Arg expr.Expr
}

func (IntaggEnumeration) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: unattached int_array_enum call")
}
