package engine

import (
	"context"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPrimaryKeyKindControls(t *testing.T) {
	e, owner, parent, _ := primaryKeyUnitFixture(t)
	ordinary := primaryKeyUnitCreate("ordinary", "ordinary_unique")
	ordinary.Elements[3].Unique.Primary = false
	withPublicSchemaUnitOwner(t, e, "pk_owner", func() {
		if _, err := e.Execute(owner, ordinary, 0, nil); err != nil {
			t.Fatal(err)
		}
		standalone := &parser.CreateIndex{Name: parser.Identifier{Text: "standalone_unique"}, Table: primaryKeyUnitName("t"), Method: "btree", Unique: true, Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "a"}}, {Column: parser.Identifier{Text: "b"}}}}
		if _, err := e.Execute(owner, standalone, 0, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, name := range []string{"ordinary_unique", "standalone_unique"} {
		index := primaryKeyUnitRelation(t, e, name)
		view := e.CatalogRead()
		metadata, _, err := projectIndexMetadata(context.Background(), view, e.catalog.Domain.Ref(view.Epoch(), index.address))
		if err != nil || !metadata.IndIsUnique || metadata.IndIsPrimary || !metadata.IndImmediate {
			t.Fatal("ordinary unique provider became primary", name, metadata, err)
		}
	}
	if !parent.UniqueConstraints[0].Primary {
		t.Fatal("unrelated provider changed retained primary")
	}
}

func TestPrimaryKeyTypedClaimInterning(t *testing.T) {
	e, owner, _, _ := primaryKeyUnitFixture(t)
	ddl := primaryKeyUnitCreate("typed", "typed_pkey")
	ddl.Elements[0].Column.TypeName = parser.TypeName{Name: "numeric"}
	width := int32(4)
	ddl.Elements[1].Column.TypeName = parser.TypeName{Name: "character", Modifiers: []parser.TypeModifier{{Integer: &width}}}
	withPublicSchemaUnitOwner(t, e, "pk_owner", func() {
		if _, err := e.Execute(owner, ddl, 0, nil); err != nil {
			t.Fatal(err)
		}
	})
	parent := primaryKeyUnitRelation(t, e, "typed")
	constraint := parent.UniqueConstraints[0]
	index := e.catalog.state.MustRelation(constraint.Index)
	if !constraint.Primary || !index.SQLIndex.Constraint.Primary {
		t.Fatal("claim fixture lacks real primary attachment")
	}
	ast := primaryKeyUnitInsert(1)
	ast.Name = primaryKeyUnitName("typed")
	ast.Rows[0][1] = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "x"}}
	scope, err := e.StartStatement(owner)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	if err := scope.gate(ast); err != nil {
		t.Fatal(err)
	}
	ordering, err := bindIndexOrdering(parent, index.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	groups := make([]sqltype.Grouping, 2)
	firstValues, equivalentValues := make([]sqltype.Value, 2), make([]sqltype.Value, 2)
	for i, spellings := range [][2]string{{"1.0", "1.00"}, {"x", "x "}} {
		usage := parent.Columns[i].Usage
		groups[i], err = sqltype.BindGrouping(usage)
		if err != nil {
			t.Fatal(err)
		}
		firstValues[i], err = usage.Type.Input(context.Background(), spellings[0], usage.Typmod)
		if err != nil {
			t.Fatal(err)
		}
		equivalentValues[i], err = usage.Type.Input(context.Background(), spellings[1], usage.Typmod)
		if err != nil {
			t.Fatal(err)
		}
	}
	ref := e.catalog.Domain.Ref(e.catalog.state.epoch, index.address)
	first, releaseFirst, err := e.pinUniqueKey(context.Background(), scope.ticket, ref, ordering, groups, firstValues)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	equivalent, releaseEquivalent, err := e.pinUniqueKey(context.Background(), scope.ticket, ref, ordering, groups, equivalentValues)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseEquivalent()
	if equivalent.Key() != first.Key() {
		t.Fatal("primary NUMERIC/BPCHAR equality split the transaction key authority")
	}
}
