package engine

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

func jsonRecordUnitCall(t *testing.T, b *analyze.Binder, name string, defined bool) functions.JSONRecord {
	t.Helper()
	b.Clause.AggregateClause = "FROM"
	b.Query.Namespace = &analyze.QueryNamespace{}
	raw := nullCountRaw(name, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: `{"a":2}`}})
	node, err := b.BindFunctionSource(raw)
	if err != nil {
		t.Fatal(err)
	}
	if defined {
		node, err = b.CompleteFunctionDefinitions(raw, node, []parser.FunctionColumnDefinition{{Name: parser.Identifier{Text: "a"}, Type: parser.TypeName{Name: "integer"}}, {Name: parser.Identifier{Text: "b"}, Type: parser.TypeName{Name: "text"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	return node.(functions.JSONRecord)
}
