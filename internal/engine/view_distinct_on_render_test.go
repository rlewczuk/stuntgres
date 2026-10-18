package engine

import (
	"context"
	"stuntgres/internal/parser"
	"testing"
)

func TestViewDistinctOnFrameOwnedSourceRendering(t *testing.T) {
	for _, key := range []string{
		"(SELECT k FROM only_key)",
		"(SELECT k FROM only_key FULL JOIN extra_key USING(k))",
		"(SELECT k FROM (SELECT k FROM only_key) AS q)",
		"(SELECT k FROM (VALUES(1)) AS q(k))",
	} {
		e := New()
		for _, q := range []string{"CREATE TABLE events(id INT)", "CREATE TABLE only_key(k INT)", "CREATE TABLE extra_key(k INT)", "CREATE VIEW v AS SELECT DISTINCT ON(" + key + ") id FROM events ORDER BY " + key + ",id DESC"} {
			run(t, e, q)
		}
		ref := roleTableRef(t, e, "v")
		definition := viewOf(e.catalog.state.MustRelation(ref.Address))
		for _, added := range []bool{false, true} {
			if added {
				run(t, e, "ALTER TABLE only_key ADD COLUMN later INT")
			}
			text, err := definition.selectSQL(context.Background(), e.CatalogRead(), viewPresentation{})
			if err != nil {
				t.Fatal(key, added, err)
			}
			rendered := statement(t, text).(*parser.Select)
			on := rendered.Distinct.On[0].(parser.SubqueryExpr).Query
			order := rendered.Order.Keys[0].Expr.(parser.SubqueryExpr).Query
			if !sameQuerySourceAliases(on.From, order.From) {
				t.Fatal("equivalent frozen query namespaces changed during SQL emission", key, added, text)
			}
			assertViewDistinctOnNestedAliases(t, on, order)
		}
	}
}

func assertViewDistinctOnNestedAliases(t *testing.T, a, b *parser.Select) {
	t.Helper()
	var walk func(parser.FromItem, parser.FromItem)
	walk = func(a, b parser.FromItem) {
		switch n := a.(type) {
		case *parser.AliasReference:
			o := b.(*parser.AliasReference)
			if n.Name.Text != o.Name.Text || len(n.Columns) != len(o.Columns) {
				t.Fatal("unstable generated RTE identity", n, o)
			}
			for i, name := range n.Columns {
				if name.Text != o.Columns[i].Text {
					t.Fatal("unstable generated column identity", n, o)
				}
			}
			walk(n.Input, o.Input)
		case *parser.JoinReference:
			o := b.(*parser.JoinReference)
			walk(n.Left, o.Left)
			walk(n.Right, o.Right)
		case *parser.DerivedQuery:
			o := b.(*parser.DerivedQuery)
			if !sameQuerySourceAliases(n.Query.From, o.Query.From) {
				t.Fatal("unstable nested query namespace", n, o)
			}
			walk(n.Query.From, o.Query.From)
		}
	}
	walk(a.From, b.From)
}
