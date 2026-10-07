package parser

import "testing"

func TestRandomRawCalls(t *testing.T) {
	for _, tc := range []struct {
		sql, name string
		arity     int
	}{
		{"SELECT random()", "random", 0},
		{"SELECT RANDOM(1,2) FROM t", "random", 2},
		{`SELECT "random"(1,2)`, "random", 2},
		{"SELECT random_normal()", "random_normal", 0},
		{"SELECT random_normal(5)", "random_normal", 1},
		{"SELECT random_normal(5,0)", "random_normal", 2},
		{"SELECT setseed($1)", "setseed", 1},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			ss, err := Parse(tc.sql)
			if err != nil || len(ss) != 1 {
				t.Fatalf("parse: %v %v", ss, err)
			}
			s := ss[0].(*Select)
			call, ok := s.Targets[0].(FunctionExpr)
			if !ok || call.Name.Parts[0].Text != tc.name || len(call.Args) != tc.arity || call.Pos.Character != 8 {
				t.Fatalf("call: %#v", s.Targets[0])
			}
			if s.IsSingleton() != (s.From == nil) {
				t.Fatal("selected singleton admission")
			}
		})
	}
	ss, err := Parse("SELECT random,random_normal,setseed FROM t")
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"random", "random_normal", "setseed"} {
		if col, ok := ss[0].(*Select).Targets[i].(ColumnExpr); !ok || col.Name.Parts[0].Text != name {
			t.Fatal(ss)
		}
	}
}
