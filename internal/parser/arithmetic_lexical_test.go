package parser

import "testing"

func TestArithmeticSymbolicBoundary(t *testing.T) {
	for _, tc := range []struct{ expr, operator string }{{"i%-1", "%-"}, {"i%+1", "%+"}, {"i**2", "**"}, {"i+-1", "+"}, {"i% -1", "%"}, {"i%/* comment */-1", "%"}, {"i%-- comment\n-1", "%"}} {
		script, err := Parse("SELECT " + tc.expr + " FROM t")
		if err != nil {
			t.Fatal(tc, err)
		}
		op, ok := script[0].(*Select).Targets[0].(OperatorExpr)
		if !ok || op.Name.Parts[0].Text != tc.operator || op.Pos.Character != 9 || len(op.Args) != 2 {
			t.Fatal(tc, op)
		}
	}
}
