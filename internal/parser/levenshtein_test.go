package parser

import (
	"strconv"
	"strings"
	"testing"
)

func TestLevenshteinNamedArguments(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		labels    []string
		arguments []string
	}{
		{"levenshtein", "source => 'a',target => 'b'", []string{"source", "target"}, []string{"'a'", "'b'"}},
		{"levenshtein", "source => 'a','b'", []string{"source", ""}, []string{"'a'", "'b'"}},
		{"levenshtein", "'a',target => 'b'", []string{"", "target"}, []string{"'a'", "'b'"}},
		{"levenshtein", "'a','b',ins_cost => 2,del_cost => 3,sub_cost => 4", []string{"", "", "ins_cost", "del_cost", "sub_cost"}, []string{"'a'", "'b'", "2", "3", "4"}},
		{"levenshtein", "'a','b',2,3,sub_cost => 4", []string{"", "", "", "", "sub_cost"}, []string{"'a'", "'b'", "2", "3", "4"}},
		{"levenshtein_less_equal", "'a','b',max_d => 5", []string{"", "", "max_d"}, []string{"'a'", "'b'", "5"}},
		{"levenshtein_less_equal", "'a','b',2,3,4,max_d => 5", []string{"", "", "", "", "", "max_d"}, []string{"'a'", "'b'", "2", "3", "4", "5"}},
		{"levenshtein", "'a',target => 'b','c'", []string{"", "target", ""}, []string{"'a'", "'b'", "'c'"}},
		{"levenshtein", "'a',target => 'b',target => 'c'", []string{"", "target", "target"}, []string{"'a'", "'b'", "'c'"}},
		// Exercise the ordinary grammar's separate function-leading routes.
		{"levenshtein", "format('%s','a'),target => 'b'", []string{"", "target"}, []string{"format('%s','a')", "'b'"}},
		{"levenshtein", "format('%s','a'),'b',2,3,sub_cost => 4", []string{"", "", "", "", "sub_cost"}, []string{"format('%s','a')", "'b'", "2", "3", "4"}},
	} {
		for _, schema := range []string{"", "public"} {
			name := tc.name
			if schema != "" {
				name = schema + "." + name
			}
			query := "SELECT " + name + "(" + tc.body + ")"
			t.Run(query, func(t *testing.T) {
				statements, err := Parse(query)
				if err != nil || len(statements) != 1 {
					t.Fatal(statements, err)
				}
				call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
				if !ok || call.Syntax != OrdinaryFunctionSyntax || len(call.Args) != len(tc.arguments) || len(call.ArgNames) != len(tc.labels) {
					t.Fatal("lost argument/label list", call)
				}
				wantParts := 1
				if schema != "" {
					wantParts = 2
				}
				if len(call.Name.Parts) != wantParts || call.Name.Parts[wantParts-1].Text != tc.name || (schema != "" && call.Name.Parts[0].Text != schema) {
					t.Fatal("lost qualified function identity", call.Name)
				}
				cursor := strings.IndexByte(query, '(') + 1
				for i, text := range tc.arguments {
					label := call.ArgNames[i]
					if label.Text != tc.labels[i] {
						t.Fatal("lost label slot", i, label, tc.labels[i])
					}
					if label.Text != "" {
						position := strings.Index(query[cursor:], label.Text+" =>") + cursor
						if label.Pos.Character != position+1 {
							t.Fatal("lost label position", i, label.Pos, position+1)
						}
					} else if label.Pos != (Position{}) {
						t.Fatal("positional prefix acquired label provenance", i, label.Pos)
					}
					offset := strings.Index(query[cursor:], text)
					if offset < 0 {
						t.Fatal("invalid test argument", text)
					}
					position := cursor + offset
					if call.Args[i].SourcePosition().Character != position+1 {
						t.Fatal("lost argument position", i, call.Args[i].SourcePosition(), position+1)
					}
					switch {
					case strings.HasPrefix(text, "format("):
						nested, ok := call.Args[i].(FunctionExpr)
						if !ok || nested.Name.Parts[len(nested.Name.Parts)-1].Text != "format" || len(nested.Args) != 2 {
							t.Fatal("lost function-leading child", call.Args[i])
						}
					case strings.HasPrefix(text, "'"):
						literal, ok := call.Args[i].(LiteralExpr)
						if !ok || literal.Value.Kind != StringValue || literal.Value.Text != strings.Trim(text, "'") {
							t.Fatal("lost string argument value/order", i, call.Args[i])
						}
					default:
						number, err := strconv.ParseInt(text, 10, 64)
						literal, ok := call.Args[i].(LiteralExpr)
						if err != nil || !ok || literal.Value.Kind != IntegerValue || literal.Value.Integer != number {
							t.Fatal("lost integer argument value/order", i, call.Args[i], err)
						}
					}
					cursor = position + len(text)
				}
			})
		}
	}
	// The added named transition must not reclassify ordinary positional calls
	// or their existing aggregate identities.
	statements, err := Parse("SELECT levenshtein('a','b'),count(*),sum(1)")
	if err != nil {
		t.Fatal(err)
	}
	targets := statements[0].(*Select).Targets
	positional, ok := targets[0].(FunctionExpr)
	if !ok || len(positional.Args) != 2 || len(positional.ArgNames) != 0 {
		t.Fatal("changed positional call", targets[0])
	}
	count, ok := targets[1].(AggregateExpr)
	if !ok || count.Name != "count" || !count.Star || len(count.Args) != 0 {
		t.Fatal("changed count identity", targets[1])
	}
	sum, ok := targets[2].(AggregateExpr)
	if !ok || sum.Name != "sum" || len(sum.Args) != 1 {
		t.Fatal("changed sum identity", targets[2])
	}
}
