package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestFunctionSourceDeclarationMetadata(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION dblink")
	enginetest.Run(t, e, "CREATE EXTENSION hstore")
	enginetest.Run(t, e, "CREATE FUNCTION metadata_record() RETURNS dblink_pkey_results LANGUAGE SQL AS 'SELECT NULL::dblink_pkey_results'")
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dblink"}))
	typ := view.MustType(ext.Members[3])
	backing := view.MustRelation(typ.OwningRelation)
	before := append([]catalog.Column(nil), backing.Columns...)
	if len(before) != 2 || !before[0].Nullable || !before[0].NullabilityKnown || !before[1].Nullable || !before[1].NullabilityKnown {
		t.Fatal("fixture lacks the genuine declared column contract", before)
	}
	null := func(name string) parser.Expr {
		return parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, TypeName: parser.TypeName{Name: name}}
	}
	// Every call returns the very same installed nominal type. Its identity and
	// backing columns cannot distinguish declaration-owning sources from ordinary
	// composite producers; the source's own metadata contract must do so.
	for _, tc := range []struct {
		name     string
		call     parser.FunctionExpr
		declared bool
	}{
		{"pkey-null", enginetest.FunctionCall("dblink_get_pkey", null("text")), true},
		{"pkey-runtime-name", enginetest.FunctionCall("dblink_get_pkey", enginetest.StringLiteral("not_resolved_during_binding")), true},
		{"json-scalar", enginetest.FunctionCall("json_populate_record", null("dblink_pkey_results"), enginetest.TypedLiteral("json", "{}")), false},
		{"json-set", enginetest.FunctionCall("json_populate_recordset", null("dblink_pkey_results"), enginetest.TypedLiteral("json", "[]")), false},
		{"hstore-scalar", enginetest.FunctionCall("populate_record", null("dblink_pkey_results"), enginetest.TypedLiteral("hstore", "")), false},
		{"routine", enginetest.FunctionCall("metadata_record"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			root, namespace, err := b.BindMixedFunctionTable([]parser.FunctionExpr{tc.call}, false, -1, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			from, ok := root.(*plan.FunctionFromNode)
			if !ok || len(from.Calls) != 1 || from.Calls[0].ResultUsage().Type != typ.SQLType {
				t.Fatalf("source lost the actual nominal result: %#v", root)
			}
			if len(namespace.Qualifiers) != 1 || namespace.Qualifiers[0].RowType != typ.SQLType {
				t.Fatal("unknown metadata erased nominal whole-row identity", namespace.Qualifiers)
			}
			if len(from.Schema()) != len(before) || len(namespace.Columns) != len(before) || len(namespace.Frame) != len(before) || len(namespace.Qualifiers[0].Columns) != len(before) {
				t.Fatal("source lost declared shape", from.Schema(), namespace)
			}
			for i, declared := range before {
				want := catalog.Column{Name: declared.Name, Usage: declared.Usage}
				if tc.declared {
					want.Nullable, want.NullabilityKnown = declared.Nullable, declared.NullabilityKnown
				}
				for _, got := range []catalog.Column{from.Schema()[i], namespace.Frame[i], namespace.Columns[i].Descriptor, namespace.Qualifiers[0].Columns[i].Descriptor} {
					if got != want {
						t.Errorf("column %d: %#v want %#v", i, got, want)
					}
				}
			}
			if !reflect.DeepEqual(backing.Columns, before) || view.MustType(ext.Members[3]).SQLType != typ.SQLType {
				t.Fatal("source binding mutated the shared declaration or nominal generation")
			}
		})
	}
}
