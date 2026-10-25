package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestFTSParserBinding(t *testing.T) {
	for _, name := range []string{"ts_parse", "ts_token_type"} {
		for _, oid := range []bool{false, true} {
			b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
			b.Clause.AggregateClause = "SELECT"
			var first parser.Expr = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "default"}}
			if oid {
				first = parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 3722}}, TypeName: parser.TypeName{Name: "oid"}}
			}
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{first}}
			if name == "ts_parse" {
				raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Word"}})
			}
			node, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(name, oid, err)
			}
			n, ok := node.(functions.FTSParser)
			if !ok || n.OID != oid || n.Parse != (name == "ts_parse") || !expr.IsSetSource(n) {
				t.Fatal("provider shape", node)
			}
			if err := n.Validate(); err != nil {
				t.Fatal(err)
			}
			if (b.Input.RegTS != nil) == oid {
				t.Fatal("text-only serial resolver capture", name, oid)
			}
			wantVol := expr.Stable
			if oid {
				wantVol = expr.Immutable
			}
			if n.Properties().Volatility != wantVol || n.IntrinsicVolatility() != wantVol {
				t.Fatal("volatility", n.Properties())
			}
			width := 3
			if name == "ts_parse" {
				width = 2
			}
			if n.ResultUsage().Type.CompositeLen() != width {
				t.Fatal("shape width")
			}
			if _, err := n.Render(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestFTSParserCatalog(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3601})
	if source.Name() != "pg_ts_parser" || len(source.Columns) != 8 || !source.History.HasIndexes {
		t.Fatal("parser relation", source)
	}
	producer, ok := pgcatalog.ProducerFor(3601)
	if !ok {
		t.Fatal("missing parser producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}
	rows, err := producer(input)
	if err != nil || len(rows) != 1 || len(rows[0]) != 8 {
		t.Fatal("parser row", rows, err)
	}
	idsExpected := []uint32{3722, 0, 11, 3717, 3718, 3719, 3720, 3721}
	for i, want := range idsExpected {
		if i == 1 {
			text, err := rows[0][i].Text()
			if err != nil || text != "default" {
				t.Fatal(text, err)
			}
			continue
		}
		var got uint32
		if i < 3 {
			got, err = rows[0][i].OID()
		} else {
			got, err = rows[0][i].ProcedureOID()
		}
		if err != nil || got != want {
			t.Fatal("identity", i, got, want, err)
		}
	}
	foreign := engine.New().CatalogRead()
	for i, c := range source.Columns {
		bad := *source
		bad.Columns = append([]catalog.Column(nil), source.Columns...)
		bad.Columns[i].Usage.Type, _ = foreign.LookupType(c.Usage.Type.ID())
		input.Source = &bad
		if rows, err := producer(input); rows != nil || err == nil {
			t.Fatal("foreign descriptor accepted", i)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = ctx
	if rows, err := producer(input); rows != nil || err != context.Canceled {
		t.Fatal("cancellation", rows, err)
	}
}
