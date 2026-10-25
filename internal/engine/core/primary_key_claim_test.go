package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
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
		idx := primaryKeyUnitRelation(t, e, name)
		view := e.CatalogRead()
		metadata, _, err := index.ProjectIndexMetadata(context.Background(), view, e.Catalog.Domain.Ref(view.Epoch(), idx.Address()))
		if err != nil || !metadata.IndIsUnique || metadata.IndIsPrimary || !metadata.IndImmediate {
			t.Fatal("ordinary unique provider became primary", name, metadata, err)
		}
	}
	if !parent.Constraints.Unique[0].Primary {
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
	constraint := parent.Constraints.Unique[0]
	idx := e.Catalog.State.MustRelation(constraint.Index)
	if !constraint.Primary || !idx.SQLIndex.Constraint.Primary {
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
	if err := scope.Gate(ast); err != nil {
		t.Fatal(err)
	}
	ordering, err := index.BindIndexOrdering(operatorFamilyTestRead(), parent, idx.SQLIndex)
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
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), idx.Address())
	first, releaseFirst, err := core.EnginePinUniqueKey(e, context.Background(), scope.LockTicket, ref, ordering, groups, firstValues)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	equivalent, releaseEquivalent, err := core.EnginePinUniqueKey(e, context.Background(), scope.LockTicket, ref, ordering, groups, equivalentValues)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseEquivalent()
	if equivalent.Key() != first.Key() {
		t.Fatal("primary NUMERIC/BPCHAR equality split the transaction key authority")
	}
}

// Exercise package entry points with independently built ASTs, not driver SQL.
// The resulting descriptors, backing roots and ACL identities are real objects.
func primaryKeyUnitFixture(t *testing.T) (*core.Engine, *session.Operation, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	admin, adminDone := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(adminDone)
	if _, err := e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: "pk_owner"}, DefaultLogin: true}, 0, nil); err != nil {
		t.Fatal(err)
	}
	owner, ownerDone := enginetest.RoleOperation(t, e, "pk_owner")
	t.Cleanup(ownerDone)
	withPublicSchemaUnitOwner(t, e, "pk_owner", func() {
		if _, err := e.Execute(owner, primaryKeyUnitCreate("t", "pk"), 0, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, a := range []int64{1, 2} {
		result, err := e.Execute(owner, primaryKeyUnitInsert(a), 0, nil)
		if err != nil || result.Count != 1 || result.Rows != nil {
			t.Fatal(result, err)
		}
	}
	parent := primaryKeyUnitRelation(t, e, "t")
	index := e.Catalog.State.MustRelation(parent.Constraints.Unique[0].Index)
	return e, owner, parent, index
}

func primaryKeyUnitCreate(name, constraint string) *parser.CreateTable {
	return &parser.CreateTable{Name: primaryKeyUnitName(name), Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "a", TypeName: parser.TypeName{Name: "integer"}}},
		{Column: &parser.Column{Name: "b", TypeName: parser.TypeName{Name: "text"}}},
		{Column: &parser.Column{Name: "secret", TypeName: parser.TypeName{Name: "text"}}},
		{Unique: &parser.UniqueConstraint{Primary: true, Name: parser.Identifier{Text: constraint}, Columns: []parser.Identifier{{Text: "a"}, {Text: "b"}}}},
	}}
}

func primaryKeyUnitName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}

func primaryKeyUnitRelation(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	address, ok := e.Catalog.State.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if !ok {
		t.Fatal("missing relation", name)
	}
	return e.Catalog.State.MustRelation(address)
}

func primaryKeyUnitInsert(a int64) *parser.Insert {
	return &parser.Insert{Name: primaryKeyUnitName("t"), Rows: [][]parser.Expr{{
		parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: a}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "protected"}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "private"}},
	}}}
}
