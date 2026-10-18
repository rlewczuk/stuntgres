package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The JSON and JSONB operators are syntax-dispatched by the engine's builtin
// operator binder; each builds a functions.Binary node with the selected
// operator's routine identity.

func jsonExtractionName(name string) bool {
	return name == "->" || name == "->>" || name == "#>" || name == "#>>"
}

func jsonbExistenceName(name string) bool { return name == "?" || name == "?|" || name == "?&" }

func jsonbContainmentName(name string) bool { return name == "@>" || name == "<@" }

// analyzeJSONOperands analyzes the written operands in the comparison
// context; operands past the second are analyzed for their errors only.
func analyzeJSONOperands(b functions.Binder, raw parser.OperatorExpr) ([2]functions.Argument, error) {
	var operands [2]functions.Argument
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return operands, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	return operands, nil
}

// textArrayUsage is the text[] formal of the path and key-list operators.
func textArrayUsage(b functions.Binder, missing string) (sqltype.Usage, error) {
	array, ok := b.ArrayType(b.Type(sqltype.Text))
	if !ok {
		return sqltype.Usage{}, fmt.Errorf("%s", missing)
	}
	return array.Usage(), nil
}

// admitsImplicit reports whether every typed operand implicitly coerces to
// its formal; untyped operands are resolved by coercion.
func admitsImplicit(operands [2]functions.Argument, targets [2]sqltype.Usage) bool {
	for i, o := range operands {
		if o.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(o.Usage, targets[i], sqltype.Implicit); err != nil {
				return false
			}
		}
	}
	return true
}

// coerceJSONOperands coerces both operands to their formals as conditional
// operands and merges their properties into base.
func coerceJSONOperands(b functions.Binder, operands [2]functions.Argument, targets [2]sqltype.Usage, name string, base expr.Properties) ([]expr.Expr, expr.Properties, error) {
	args := make([]expr.Expr, 2)
	properties := base
	for i, o := range operands {
		arg, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: targets[i], Label: name})
		if err != nil {
			return nil, expr.Properties{}, err
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	return args, properties, nil
}

// jsonOperatorNode builds the Binary node and attaches the operator identity.
func jsonOperatorNode(b functions.Binder, raw parser.OperatorExpr, name string, result sqltype.Usage, args []expr.Expr, properties expr.Properties, left, right sqltype.ID) (expr.Expr, error) {
	info := b.Info(result, raw.Pos, properties)
	node, err := functions.NewBinary(info, name, args, parser.OrdinaryFunctionSyntax)
	if err != nil {
		return nil, err
	}
	info, err = b.BuiltinOperatorRoutineInfo(info, name, left, right)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}

func bindJSONExtraction(b functions.Binder, raw parser.OperatorExpr) (expr.Expr, error) {
	operands, err := analyzeJSONOperands(b, raw)
	if err != nil {
		return nil, err
	}
	left, right := operands[0].Usage.Type, operands[1].Usage.Type
	signature := func() error { return functions.ArrayOperatorError(b, left, right, raw) }
	if len(raw.Args) != 2 {
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), left, right, raw)
	}
	visible, err := b.BuiltinOperatorVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, signature()
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	rhs := b.Type(sqltype.Text).Usage()
	if name == "#>" || name == "#>>" {
		rhs, err = textArrayUsage(b, "missing text array")
		if err != nil {
			return nil, err
		}
	} else if right != nil && (right.BaseType().ID() == sqltype.Int2 || right.BaseType().ID() == sqltype.Int4) {
		rhs = b.Type(sqltype.Int4).Usage()
	}
	document := b.Type(sqltype.JSON).Usage()
	if left != nil && left.BaseType().ID() == sqltype.JSONB {
		document = b.Type(sqltype.JSONB).Usage()
	}
	targets := [2]sqltype.Usage{document, rhs}
	if !admitsImplicit(operands, targets) {
		return nil, signature()
	}
	if left == nil {
		return nil, functions.ComparisonError(b, sqlError("42725", "operator is not unique"), left, right, raw)
	}
	args, properties, err := coerceJSONOperands(b, operands, targets, name, expr.Properties{Volatility: expr.Immutable})
	if err != nil {
		return nil, err
	}
	result := document
	if name == "->>" || name == "#>>" {
		result = b.Type(sqltype.Text).Usage()
	}
	return jsonOperatorNode(b, raw, name, result, args, properties, document.Type.ID(), rhs.Type.ID())
}

func bindJSONBExistence(b functions.Binder, raw parser.OperatorExpr) (expr.Expr, error) {
	operands, err := analyzeJSONOperands(b, raw)
	if err != nil {
		return nil, err
	}
	left, right := operands[0].Usage.Type, operands[1].Usage.Type
	visible, err := b.BuiltinOperatorVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	signature := func() error { return functions.ArrayOperatorError(b, left, right, raw) }
	// Unary spelling is parsed by the existing generic-operator grammar but
	// has no selected executable signature.
	if len(raw.Args) != 2 {
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), left, right, raw)
	}
	if !visible {
		return nil, signature()
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if name == "?|" && left == nil && right == nil {
		return nil, functions.ComparisonError(b, sqlError("42725", "operator is not unique"), left, right, raw)
	}
	targets := [2]sqltype.Usage{b.Type(sqltype.JSONB).Usage(), b.Type(sqltype.Text).Usage()}
	if name != "?" {
		targets[1], err = textArrayUsage(b, "stuntgres: missing text array association")
		if err != nil {
			return nil, err
		}
	}
	if !admitsImplicit(operands, targets) {
		return nil, signature()
	}
	args, properties, err := coerceJSONOperands(b, operands, targets, name, expr.Properties{})
	if err != nil {
		return nil, err
	}
	return functions.NewBinary(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), name, args, parser.OrdinaryFunctionSyntax)
}

func bindJSONBPathDelete(b functions.Binder, raw parser.OperatorExpr) (expr.Expr, error) {
	operands, err := analyzeJSONOperands(b, raw)
	if err != nil {
		return nil, err
	}
	left, right := operands[0].Usage.Type, operands[1].Usage.Type
	visible, err := b.BuiltinOperatorVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) != 2 {
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), left, right, raw)
	}
	if !visible {
		return nil, functions.ArrayOperatorError(b, left, right, raw)
	}
	array, err := textArrayUsage(b, "stuntgres: missing text array association")
	if err != nil {
		return nil, err
	}
	targets := [2]sqltype.Usage{b.Type(sqltype.JSONB).Usage(), array}
	if !admitsImplicit(operands, targets) {
		return nil, functions.ArrayOperatorError(b, left, right, raw)
	}
	args, properties, err := coerceJSONOperands(b, operands, targets, "#-", expr.Properties{})
	if err != nil {
		return nil, err
	}
	return jsonOperatorNode(b, raw, "#-", targets[0], args, properties, targets[0].Type.ID(), targets[1].Type.ID())
}

// selectJSONBConcat is the JSONB branch of the || selector. Its operands have
// already been analyzed; it selects only when one is known JSONB and the
// other is JSONB or unknown.
func selectJSONBConcat(b functions.Binder, raw parser.OperatorExpr, operands [2]functions.Argument, visible bool) (expr.Expr, bool, error) {
	known := false
	for _, o := range operands {
		if o.Usage.Type != nil {
			if o.Usage.Type.BaseType().ID() != sqltype.JSONB {
				return nil, false, nil
			}
			known = true
		}
	}
	if !visible || !known {
		return nil, false, nil
	}
	node, err := jsonbOperatorNode(b, raw, operands, sqltype.JSONB)
	return node, true, err
}

// selectJSONBDelete selects deletion with a known JSONB left operand, or an
// unknown left operand and a known right operand implicitly coercible to
// TEXT. Unknown-left/integer stays numeric; both unknown operands retain
// arithmetic ambiguity.
func selectJSONBDelete(b functions.Binder, raw parser.OperatorExpr, left, right functions.Argument) (expr.Expr, bool, error) {
	lt, rt := left.Usage.Type, right.Usage.Type
	knownLeft := lt != nil && lt.BaseType().ID() == sqltype.JSONB
	rhs := sqltype.Text
	if !knownLeft {
		if lt != nil || rt == nil {
			return nil, false, nil
		}
		if _, err := sqltype.PlanCoercion(right.Usage, b.Type(sqltype.Text).Usage(), sqltype.Implicit); err != nil {
			return nil, false, nil
		}
	} else if rt != nil {
		if rt.BaseType().ID() == sqltype.Int2 || rt.BaseType().ID() == sqltype.Int4 {
			rhs = sqltype.Int4
		}
		if _, err := sqltype.PlanCoercion(right.Usage, b.Type(rhs).Usage(), sqltype.Implicit); err != nil {
			return nil, true, functions.OperatorError(sqlError("42883", "operator does not exist"), lt, rt, raw)
		}
	}
	node, err := jsonbOperatorNode(b, raw, [2]functions.Argument{left, right}, rhs)
	return node, true, err
}

func jsonbOperatorNode(b functions.Binder, raw parser.OperatorExpr, operands [2]functions.Argument, rhs sqltype.ID) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	targets := [2]sqltype.Usage{b.Type(sqltype.JSONB).Usage(), b.Type(rhs).Usage()}
	args, properties, err := coerceJSONOperands(b, operands, targets, name, expr.Properties{})
	if err != nil {
		return nil, err
	}
	return jsonOperatorNode(b, raw, name, targets[0], args, properties, sqltype.JSONB, rhs)
}

// bindJSONBContainment binds jsonb @>/<@. Operands have already been
// analyzed and operator visibility checked by the shared array/JSONB
// spelling dispatcher. Do not analyze occurrences twice.
func bindJSONBContainment(b functions.Binder, raw parser.OperatorExpr, operands [2]functions.Argument) (expr.Expr, error) {
	target := b.Type(sqltype.JSONB).Usage()
	if !admitsImplicit(operands, [2]sqltype.Usage{target, target}) {
		return nil, functions.ArrayOperatorError(b, operands[0].Usage.Type, operands[1].Usage.Type, raw)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args, properties, err := coerceJSONOperands(b, operands, [2]sqltype.Usage{target, target}, name, expr.Properties{})
	if err != nil {
		return nil, err
	}
	return jsonOperatorNode(b, raw, name, b.Type(sqltype.Bool).Usage(), args, properties, sqltype.JSONB, sqltype.JSONB)
}
