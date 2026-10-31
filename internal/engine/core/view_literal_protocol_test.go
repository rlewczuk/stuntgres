package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func protocolBinder(t *testing.T) *analyze.Binder {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	column := catalog.Column{Name: "k", Usage: b.Type(sqltype.Text).Usage()}
	b.Query.Namespace = &analyze.QueryNamespace{Columns: []analyze.QueryColumn{{Descriptor: column, Slot: 0, Leaf: -1, Physical: -1}}, Frame: []catalog.Column{column}}
	return b
}

func protocolBind(t *testing.T, b *analyze.Binder, query string) (parser.Expr, expr.Expr) {
	t.Helper()
	raw := enginetest.Statement(t, query).(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(query, err)
	}
	return raw, node
}

func jsonObjectUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}
