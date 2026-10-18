package functions

import (
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestCallLabelErrorOrder(t *testing.T) {
	arg := func(at int) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Pos: parser.Position{Character: at}}}
	}
	label := func(text string, at int) parser.Identifier {
		return parser.Identifier{Text: text, Pos: parser.Position{Character: at}}
	}
	cases := []struct {
		name string
		raw  parser.FunctionExpr
		code string
		pos  string
	}{
		{"unlabelled", parser.FunctionExpr{Args: []parser.Expr{arg(3)}}, "", ""},
		{"positional after named", parser.FunctionExpr{Args: []parser.Expr{arg(3), arg(9)}, ArgNames: []parser.Identifier{label("a", 1), label("", 9)}}, "42601", "9"},
		{"duplicate", parser.FunctionExpr{Args: []parser.Expr{arg(3), arg(9)}, ArgNames: []parser.Identifier{label("a", 1), label("a", 7)}}, "42601", "7"},
		// A positional argument after a name precedes a later duplicate.
		{"positional before duplicate", parser.FunctionExpr{Args: []parser.Expr{arg(3), arg(5), arg(9)}, ArgNames: []parser.Identifier{label("a", 1), label("", 5), label("a", 7)}}, "42601", "5"},
	}
	for _, c := range cases {
		err := CallLabelError(c.raw)
		var diagnostic *sqlstate.Error
		switch {
		case c.code == "" && err != nil:
			t.Fatalf("%s: unexpected error %v", c.name, err)
		case c.code != "" && (!errors.As(err, &diagnostic) || diagnostic.Code != c.code || diagnostic.Position != c.pos):
			t.Fatalf("%s: got %#v, want %s at %s", c.name, err, c.code, c.pos)
		}
	}
	malformed := parser.FunctionExpr{Args: []parser.Expr{arg(3)}, ArgNames: []parser.Identifier{label("a", 1), label("b", 2)}}
	if err := CallLabelError(malformed); err == nil || errors.As(err, new(*sqlstate.Error)) {
		t.Fatalf("malformed labels: got %v, want an internal error", err)
	}
}
