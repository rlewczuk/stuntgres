package parser

import (
	"context"
	"errors"
	"testing"
)

func TestRoutineDeclarationSyntaxBoundary(t *testing.T) {
	for _, name := range []string{"sin", "asin", "cos"} {
		t.Run(name, func(t *testing.T) {
			source := "SELECT 2 * " + name + "($1)"
			ordinary, err := Parse(source)
			if err == nil || len(ordinary) != 0 {
				t.Fatal("ordinary execution frontier widened", ordinary, err)
			}
			statements, err := ParseRoutineDeclarationBody(source)
			if err != nil || len(statements) != 1 {
				t.Fatal("readonly genuine declaration syntax rejected", statements, err)
			}
			query, ok := statements[0].(*Select)
			if !ok || len(query.Targets) != 1 {
				t.Fatalf("lost actual SELECT target: %T", statements[0])
			}
			product, ok := query.Targets[0].(OperatorExpr)
			if !ok || len(product.Name.Parts) != 1 || product.Name.Parts[0].Text != "*" || len(product.Args) != 2 {
				t.Fatalf("lost arithmetic syntax: %#v", query.Targets[0])
			}
			literal, ok := product.Args[0].(LiteralExpr)
			if !ok || literal.Value.Kind != IntegerValue || literal.Value.Integer != 2 {
				t.Fatal("lost literal left operand", product.Args[0])
			}
			call, ok := product.Args[1].(FunctionExpr)
			if !ok || len(call.Name.Parts) != 1 || call.Name.Parts[0].Text != name || len(call.Args) != 1 {
				t.Fatal("lost genuine selected-call right operand", product.Args[1])
			}
			parameter, ok := call.Args[0].(ParameterExpr)
			if !ok || parameter.Number != 1 {
				t.Fatal("lost declaration parameter", call.Args[0])
			}
			// Readonly mode belongs to this parse, never a global parser switch.
			if again, err := ParseWithOptions(source, Options{StandardConformingStrings: true}); err == nil || len(again) != 0 {
				t.Fatal("readonly mode escaped into later ordinary parse", again, err)
			}
		})
	}
	for _, source := range []string{
		"SELECT 2 * sin(",
		"SELECT 2 * sin($1); unexpected",
		"SELECT 2 * sin($1)\x00",
		"SELECT 2 * sin($1)\xff",
	} {
		if statements, err := ParseRoutineDeclarationBody(source); err == nil || len(statements) != 0 {
			t.Fatal("readonly capture bypassed ordinary syntax/input validation", source, statements, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if statements, err := parseWithOptions("SELECT 2 * sin($1)", Options{Context: ctx, StandardConformingStrings: true}, true); !errors.Is(err, context.Canceled) || len(statements) != 0 {
		t.Fatal("readonly parse bypassed cancellation", statements, err)
	}
	if statements, err := parseWithOptions("SELECT 2 * sin($2)", Options{StandardConformingStrings: true, ClosedRoutineFormals: true, FormalCount: 1}, true); err == nil || len(statements) != 0 {
		t.Fatal("readonly parse bypassed closed formal bounds", statements, err)
	}
}
