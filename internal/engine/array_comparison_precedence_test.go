package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestArrayCompareDiagnosticPrecedence(t *testing.T) {
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	e := New()
	for _, tc := range []struct{ query, code, message, position, hint string }{
		{"SELECT 'badleft' IN (1,'badright')", "22P02", `invalid input syntax for type integer: "badright"`, "24", ""},
		{"SELECT 'badleft' NOT IN (1,'badright')", "22P02", `invalid input syntax for type integer: "badright"`, "28", ""},
		{"SELECT 'badleft' IN ('badfirst',1,'badlast')", "22P02", `invalid input syntax for type integer: "badfirst"`, "22", ""},
		{"SELECT 'badleft' NOT IN ('badfirst',1,'badlast')", "22P02", `invalid input syntax for type integer: "badfirst"`, "26", ""},
		{"SELECT '{}'::JSON = ANY('oops')", "42883", "operator does not exist: json = unknown", "19", hint},
		{"SELECT '{}'::JSON = SOME('oops')", "42883", "operator does not exist: json = unknown", "19", hint},
		{"SELECT '{}'::JSON = ALL('oops')", "42883", "operator does not exist: json = unknown", "19", hint},
		{"SELECT '{}'::JSON != SOME('oops')", "42883", "operator does not exist: json <> unknown", "19", hint},
		// Successful type-only admission must not hide real codec failures.
		{"SELECT 'oops' = ANY('{1}'::INT[])", "22P02", `invalid input syntax for type integer: "oops"`, "8", ""},
		{"SELECT 1 = ANY('oops')", "22P02", `malformed array literal: "oops"`, "16", ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := statement(t, tc.query).(*parser.Select).Targets[0]
			_, err := b.bind(raw, expressionPolicy{context: projectionContext})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Position != tc.position || diagnostic.Hint != tc.hint {
				t.Fatal(err, tc)
			}
		})
	}
	// A known scalar lhs keeps its own provider signature when the RHS batch
	// selects a wider common type. SQL parameter inference is not rewritten.
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT $1::INT IN(1,2147483648)").(*parser.Select).Targets[0]
	bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	node := bound.(scalarArrayComparisonExpression)
	if node.Left.ResultUsage().Type.ID() != 23 || node.Items[0].ResultUsage().Type.ID() != 20 || b.params.parameters[1].Type.ID() != 23 {
		t.Fatal("batch erased established scalar signature", node.Left.ResultUsage(), node.Items[0].ResultUsage(), b.params.parameters)
	}
}
