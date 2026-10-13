package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestArraySearchParser(t *testing.T) {
	for _, name := range []string{"array_position", "array_positions", "array_remove", "array_replace"} {
		args := "ARRAY[1],1"
		if name == "array_replace" {
			args += ",2"
		}
		query := "SELECT " + name + "(" + args + ")"
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(query, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != name || call.Pos.Character != 8 || len(call.Args) < 2 {
			t.Fatalf("call=%#v", statements[0])
		}
	}
	for _, tc := range []struct{ query, operator string }{
		{"SELECT array_remove(ARRAY[1,2],2) @> ARRAY[1]", "@>"},
		{"SELECT ARRAY[1] <@ array_replace(ARRAY[2],2,1)", "<@"},
		{"SELECT /*é*/array_remove(ARRAY[1,2],2) && array_replace(ARRAY[2],2,1)", "&&"},
		{"SELECT ARRAY[1] OPERATOR(pg_catalog.@>) ARRAY[1,1]", "@>"},
	} {
		statements, err := Parse(tc.query)
		if err != nil {
			t.Fatal(tc.query, err)
		}
		op, ok := statements[0].(*Select).Targets[0].(OperatorExpr)
		if !ok || len(op.Args) != 2 || op.Name.Parts[len(op.Name.Parts)-1].Text != tc.operator {
			t.Fatalf("operator=%#v", statements[0])
		}
		if !strings.Contains(tc.query, "OPERATOR(") {
			want := utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.operator)]) + 1
			if op.Pos.Character != want {
				t.Fatalf("position=%d want %d", op.Pos.Character, want)
			}
		}
	}
	for _, query := range []string{
		"SELECT array_remove(ARRAY[1],1)::int[]",
		"SELECT ARRAY[array_position(ARRAY[1],1)]",
		"SELECT array_replace(array_remove(ARRAY[1,2],1),2,3)",
		"SELECT array_position(DISTINCT ARRAY[1],1)",
		"SELECT array_position(ARRAY[1],1) OVER ()",
		"SELECT array_position(arr => ARRAY[1],elem => 1)",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
}
