package analyze

import (
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSQLJSONDefaultShape(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, Number: 1}
	rawParam := parser.ParameterExpr{Number: 1}
	rawCast := parser.CastExpr{Child: rawParam}
	coerce := func(from, to sqltype.Usage, child expr.Expr) expr.Expr {
		t.Helper()
		conversion, err := sqltype.PlanCoercion(from, to, sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		return plan.CoercionExpr{Info: expr.Info{Usage: to}, Child: child, Plan: conversion}
	}
	operator := parser.OperatorExpr{Args: []parser.Expr{rawParam}}
	functionCast := coerce(parameter.ResultUsage(), types[sqltype.Int8].Usage(), parameter)
	varchar := types[sqltype.VarChar].Usage()
	sized := varchar
	sized.Typmod = 7
	textParam := plan.ParameterExpr{Info: expr.Info{Usage: types[sqltype.Text].Usage()}, Number: 1}
	domain, err := sqltype.Domain(90001, "default_int", "default_int", parameter.ResultUsage())
	if err != nil {
		t.Fatal(err)
	}
	intArray, err := sqltype.Array(90002, "_default_int", types[sqltype.Int4])
	if err != nil {
		t.Fatal(err)
	}
	bigArray, err := sqltype.Array(90003, "_default_big", types[sqltype.Int8])
	if err != nil {
		t.Fatal(err)
	}
	arrayParam := plan.ParameterExpr{Info: expr.Info{Usage: intArray.Usage()}, Number: 1}
	rawRegclass := parser.RegclassExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "default_sequence"}}}
	regclassUsage := types[sqltype.Regclass].Usage()
	regclassConstant := plan.LiteralExpr{Info: expr.Info{Usage: regclassUsage}, Value: sqltype.String(regclassUsage.Type, "90004")}
	wrapRegclass := func(raw parser.Expr, bound expr.Expr) (parser.Expr, expr.Expr) {
		t.Helper()
		raw = parser.CastExpr{Child: parser.RegclassExpr{Child: parser.CastExpr{Child: raw}}}
		bound = coerce(bound.ResultUsage(), types[sqltype.Oid].Usage(), bound)
		bound = coerce(bound.ResultUsage(), regclassUsage, bound)
		bound = coerce(bound.ResultUsage(), types[sqltype.Text].Usage(), bound)
		return raw, bound
	}
	intOperator := plan.ArithmeticExpr{Info: parameter.Info, Left: parameter, Right: parameter}
	wrappedOperatorRaw, wrappedOperator := wrapRegclass(operator, intOperator)
	wrappedParamRaw, wrappedParam := wrapRegclass(rawParam, parameter)
	special := plan.ConditionalExpr{Info: parameter.Info, Args: []expr.Expr{parameter}}
	wrappedSpecialRaw, wrappedSpecial := wrapRegclass(parser.ConditionalExpr{Args: []parser.Expr{rawParam}}, special)
	cases := []struct {
		name  string
		raw   parser.Expr
		bound expr.Expr
		valid bool
	}{
		{"bare parameter", rawParam, parameter, false},
		{"elided cast", rawCast, parameter, false},
		{"elided type-name call", parser.CallExpr{Args: []parser.Expr{rawParam}}, parameter, false},
		{"identity wrapper", rawCast, coerce(parameter.ResultUsage(), parameter.ResultUsage(), parameter), false},
		{"function cast", rawCast, functionCast, true},
		{"io wrapper", rawCast, coerce(parameter.ResultUsage(), types[sqltype.Text].Usage(), parameter), false},
		{"io around function cast", rawCast, coerce(functionCast.ResultUsage(), types[sqltype.Text].Usage(), functionCast), true},
		{"operator parameter", operator, plan.ComparisonExpr{Left: parameter, Right: parameter}, true},
		{"binary string relabel", rawCast, coerce(textParam.ResultUsage(), varchar, textParam), false},
		{"typmod function", rawCast, coerce(textParam.ResultUsage(), sized, textParam), true},
		{"collation wrapper", parser.CollateExpr{Child: rawParam}, plan.CollationExpr{Child: textParam}, false},
		{"special conditional", parser.ConditionalExpr{}, parameter, false},
		{"array constructor", parser.ArrayExpr{}, parameter, false},
		{"domain wrapper", rawCast, coerce(parameter.ResultUsage(), domain.Usage(), parameter), false},
		{"domain around function cast", rawCast, coerce(functionCast.ResultUsage(), domain.Usage(), functionCast), true},
		{"array element function", rawCast, coerce(intArray.Usage(), bigArray.Usage(), arrayParam), true},
		{"specialized regclass constant", rawRegclass, regclassConstant, true},
		{"io around regclass constant", parser.CastExpr{Child: rawRegclass}, coerce(regclassUsage, types[sqltype.Text].Usage(), regclassConstant), true},
		{"specialized regclass parameter", parser.RegclassExpr{Child: rawParam}, plan.ParameterExpr{Info: expr.Info{Usage: regclassUsage}, Number: 1}, false},
		{"specialized regclass nonliteral operator", wrappedOperatorRaw, wrappedOperator, true},
		{"specialized binary and IO parameter", wrappedParamRaw, wrappedParam, false},
		{"specialized binary and IO conditional", wrappedSpecialRaw, wrappedSpecial, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sqlJSONDefaultExpression(tc.raw, tc.bound); got != tc.valid {
				t.Fatalf("DEFAULT shape = %v, want %v", got, tc.valid)
			}
		})
	}

	t.Run("shape then full bound tree columns and transformed positions", func(t *testing.T) {
		for _, shift := range []int{0, 7} { // ASCII and /*é🙂*/ character offsets.
			columnPos := parser.Position{Offset: 90 + shift, Character: 91 + shift}
			operatorPos := parser.Position{Offset: 94 + shift, Character: 95 + shift}
			functionPos := parser.Position{Offset: 89 + shift, Character: 90 + shift}
			columnRaw := parser.ColumnExpr{Pos: columnPos}
			column := plan.ColumnExpr{Info: expr.Info{Usage: parameter.ResultUsage(), Pos: columnPos}, Frame: 3, Slot: 7}
			rawOperator := parser.OperatorExpr{Pos: operatorPos, Args: []parser.Expr{columnRaw, parser.ParameterExpr{Number: 1, Pos: parser.Position{Offset: 96 + shift, Character: 97 + shift}}}}
			boundOperator := plan.ArithmeticExpr{Info: expr.Info{Usage: parameter.ResultUsage(), Pos: operatorPos}, Left: parameter, Right: coerce(column.ResultUsage(), column.ResultUsage(), column)}
			rawWrapped, boundWrapped := wrapRegclass(rawOperator, boundOperator)
			conditional := plan.ConditionalExpr{Info: expr.Info{Usage: parameter.ResultUsage(), Pos: functionPos}, Args: []expr.Expr{parameter, boundOperator}}
			rawSpecial := parser.ConditionalExpr{Pos: functionPos, Args: []parser.Expr{rawOperator}}
			// A genuine scalar cast function provides function shape while the
			// reference is nested in an unselected-capable special descendant.
			castFunction := coerce(conditional.ResultUsage(), types[sqltype.Int8].Usage(), conditional)
			outerOperator := plan.ArithmeticExpr{Info: expr.Info{Usage: parameter.ResultUsage(), Pos: operatorPos}, Left: parameter, Right: conditional}
			mathPlan, err := sqltype.BindMath("abs", parameter.ResultUsage(), parameter.ResultUsage())
			if err != nil {
				t.Fatal(err)
			}
			function := functions.Math{Info: expr.Info{Usage: parameter.ResultUsage(), Pos: functionPos}, Plan: mathPlan, Args: []expr.Expr{conditional}}
			for _, tc := range []struct {
				name    string
				raw     parser.Expr
				bound   expr.Expr
				message string
				pos     parser.Position
			}{
				{"operator wrapped correlated right descendant", rawWrapped, boundWrapped, "DEFAULT expression must not contain column references", columnPos},
				{"cast function nested column", parser.CastExpr{Child: rawSpecial}, castFunction, "DEFAULT expression must not contain column references", functionPos},
				{"ordinary function nested column", parser.FunctionExpr{Pos: functionPos, Args: []parser.Expr{rawSpecial}}, function, "DEFAULT expression must not contain column references", functionPos},
				{"operator nested special column", rawOperator, outerOperator, "DEFAULT expression must not contain column references", columnPos},
				{"bare column shape wins", columnRaw, column, "can only specify a constant, non-aggregate function, or operator expression for DEFAULT", columnPos},
				{"special shape wins", rawSpecial, conditional, "can only specify a constant, non-aggregate function, or operator expression for DEFAULT", functionPos},
			} {
				t.Run(tc.name+"/"+strconv.Itoa(shift), func(t *testing.T) {
					var b Binder
					err := b.ValidateSQLJSONDefault(tc.raw, tc.bound)
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" || diagnostic.Message != tc.message || diagnostic.Position != strconv.Itoa(tc.pos.Character) {
						t.Fatalf("DEFAULT diagnostic = %#v (%v), want 42804 %q at %d", diagnostic, err, tc.message, tc.pos.Character)
					}
				})
			}
			var b Binder
			if err := b.ValidateSQLJSONDefault(wrappedOperatorRaw, wrappedOperator); err != nil {
				t.Fatalf("parameter-only operator rejected: %v", err)
			}
		}
	})
}
