package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The JSON and JSONB operators are syntax-dispatched by the engine's builtin
// operator binder, which calls these entry points; each builds a Binary node
// with the selected operator's routine identity.

// JSONExtractionName reports the json/jsonb extraction operator spellings.
func JSONExtractionName(name string) bool {
	return name == "->" || name == "->>" || name == "#>" || name == "#>>"
}

// JSONBExistenceName reports the jsonb key-existence operator spellings.
func JSONBExistenceName(name string) bool { return name == "?" || name == "?|" || name == "?&" }

// JSONBContainmentName reports the containment spellings that jsonb shares
// with arrays.
func JSONBContainmentName(name string) bool { return name == "@>" || name == "<@" }

// JSONBPathOperatorName reports the JSONPath existence and match spellings.
func JSONBPathOperatorName(name string) bool { return name == "@?" || name == "@@" }

// BindJSONBPathOperator receives the catalog-selected JSONB/jsonpath signature.
// The engine leaves competing @@ text-search signatures with their own provider.
func BindJSONBPathOperator(b Binder, raw parser.OperatorExpr) (expr.Expr, error) {
	operands, err := analyzeJSONOperands(b, raw)
	if err != nil {
		return nil, err
	}
	targets := [2]sqltype.Usage{b.Type(sqltype.JSONB).Usage(), b.Type(sqltype.JSONPath).Usage()}
	if len(raw.Args) != 2 || !admitsImplicit(operands, targets) {
		return nil, ArrayOperatorError(b, operands[0].Usage.Type, operands[1].Usage.Type, raw)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args, properties, err := coerceJSONOperands(b, operands, targets, name, expr.Properties{Volatility: expr.Immutable})
	if err != nil {
		return nil, err
	}
	return jsonOperatorNode(b, raw, name, b.Type(sqltype.Bool).Usage(), args, properties, sqltype.JSONB, sqltype.JSONPath)
}

// analyzeJSONOperands analyzes the written operands in the comparison
// context; operands past the second are analyzed for their errors only.
func analyzeJSONOperands(b Binder, raw parser.OperatorExpr) ([2]Argument, error) {
	var operands [2]Argument
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: ComparisonArgument})
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
func textArrayUsage(b Binder, missing string) (sqltype.Usage, error) {
	array, ok := b.ArrayType(b.Type(sqltype.Text))
	if !ok {
		return sqltype.Usage{}, fmt.Errorf("%s", missing)
	}
	return array.Usage(), nil
}

// admitsImplicit reports whether every typed operand implicitly coerces to
// its formal; untyped operands are resolved by coercion.
func admitsImplicit(operands [2]Argument, targets [2]sqltype.Usage) bool {
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
func coerceJSONOperands(b Binder, operands [2]Argument, targets [2]sqltype.Usage, name string, base expr.Properties) ([]expr.Expr, expr.Properties, error) {
	args := make([]expr.Expr, 2)
	properties := base
	for i, o := range operands {
		arg, err := b.CoerceArgument(o, CoercionRequest{Intent: ConditionalArgument, Target: targets[i], Label: name})
		if err != nil {
			return nil, expr.Properties{}, err
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	return args, properties, nil
}

// jsonOperatorNode builds the Binary node and attaches the operator identity.
func jsonOperatorNode(b Binder, raw parser.OperatorExpr, name string, result sqltype.Usage, args []expr.Expr, properties expr.Properties, left, right sqltype.ID) (expr.Expr, error) {
	info := b.Info(result, raw.Pos, properties)
	node, err := NewBinary(info, name, args, parser.OrdinaryFunctionSyntax)
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

// BindJSONExtraction binds ->, ->>, #> and #>> over json or jsonb.
func BindJSONExtraction(b Binder, raw parser.OperatorExpr) (expr.Expr, error) {
	operands, err := analyzeJSONOperands(b, raw)
	if err != nil {
		return nil, err
	}
	left, right := operands[0].Usage.Type, operands[1].Usage.Type
	signature := func() error { return ArrayOperatorError(b, left, right, raw) }
	if len(raw.Args) != 2 {
		return nil, OperatorError(sqlError("42883", "operator does not exist"), left, right, raw)
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
		return nil, ComparisonError(b, sqlError("42725", "operator is not unique"), left, right, raw)
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

// BindJSONBExistence binds the jsonb ?, ?| and ?& operators.
func BindJSONBExistence(b Binder, raw parser.OperatorExpr) (expr.Expr, error) {
	operands, err := analyzeJSONOperands(b, raw)
	if err != nil {
		return nil, err
	}
	left, right := operands[0].Usage.Type, operands[1].Usage.Type
	visible, err := b.BuiltinOperatorVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	signature := func() error { return ArrayOperatorError(b, left, right, raw) }
	// Unary spelling is parsed by the existing generic-operator grammar but
	// has no selected executable signature.
	if len(raw.Args) != 2 {
		return nil, OperatorError(sqlError("42883", "operator does not exist"), left, right, raw)
	}
	if !visible {
		return nil, signature()
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if name == "?|" && left == nil && right == nil {
		return nil, ComparisonError(b, sqlError("42725", "operator is not unique"), left, right, raw)
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
	return NewBinary(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), name, args, parser.OrdinaryFunctionSyntax)
}

// BindJSONBPathDelete binds jsonb #- text[].
func BindJSONBPathDelete(b Binder, raw parser.OperatorExpr) (expr.Expr, error) {
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
		return nil, OperatorError(sqlError("42883", "operator does not exist"), left, right, raw)
	}
	if !visible {
		return nil, ArrayOperatorError(b, left, right, raw)
	}
	array, err := textArrayUsage(b, "stuntgres: missing text array association")
	if err != nil {
		return nil, err
	}
	targets := [2]sqltype.Usage{b.Type(sqltype.JSONB).Usage(), array}
	if !admitsImplicit(operands, targets) {
		return nil, ArrayOperatorError(b, left, right, raw)
	}
	args, properties, err := coerceJSONOperands(b, operands, targets, "#-", expr.Properties{})
	if err != nil {
		return nil, err
	}
	return jsonOperatorNode(b, raw, "#-", targets[0], args, properties, targets[0].Type.ID(), targets[1].Type.ID())
}

// SelectJSONBConcat is the JSONB branch of the || selector. Its operands have
// already been analyzed; it selects only when one is known JSONB and the
// other is JSONB or unknown.
func SelectJSONBConcat(b Binder, raw parser.OperatorExpr, operands [2]Argument, visible bool) (expr.Expr, bool, error) {
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

// SelectJSONBDelete selects deletion with a known JSONB left operand, or an
// unknown left operand and a known right operand implicitly coercible to
// TEXT. Unknown-left/integer stays numeric; both unknown operands retain
// arithmetic ambiguity.
func SelectJSONBDelete(b Binder, raw parser.OperatorExpr, left, right Argument) (expr.Expr, bool, error) {
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
			return nil, true, OperatorError(sqlError("42883", "operator does not exist"), lt, rt, raw)
		}
	}
	node, err := jsonbOperatorNode(b, raw, [2]Argument{left, right}, rhs)
	return node, true, err
}

func jsonbOperatorNode(b Binder, raw parser.OperatorExpr, operands [2]Argument, rhs sqltype.ID) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	targets := [2]sqltype.Usage{b.Type(sqltype.JSONB).Usage(), b.Type(rhs).Usage()}
	args, properties, err := coerceJSONOperands(b, operands, targets, name, expr.Properties{})
	if err != nil {
		return nil, err
	}
	return jsonOperatorNode(b, raw, name, targets[0], args, properties, sqltype.JSONB, rhs)
}

// BindJSONBContainment binds jsonb @>/<@. Operands have already been
// analyzed and operator visibility checked by the shared array/JSONB
// spelling dispatcher. Do not analyze occurrences twice.
func BindJSONBContainment(b Binder, raw parser.OperatorExpr, operands [2]Argument) (expr.Expr, error) {
	target := b.Type(sqltype.JSONB).Usage()
	if !admitsImplicit(operands, [2]sqltype.Usage{target, target}) {
		return nil, ArrayOperatorError(b, operands[0].Usage.Type, operands[1].Usage.Type, raw)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args, properties, err := coerceJSONOperands(b, operands, [2]sqltype.Usage{target, target}, name, expr.Properties{})
	if err != nil {
		return nil, err
	}
	return jsonOperatorNode(b, raw, name, b.Type(sqltype.Bool).Usage(), args, properties, sqltype.JSONB, sqltype.JSONB)
}
