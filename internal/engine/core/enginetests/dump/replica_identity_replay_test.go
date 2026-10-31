package dump_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestReplicaIdentityNativeReplay(t *testing.T) {
	e, s, parent := replicaUnitFixture(t)
	enginetest.PreparedUnitStatement(t, e, s, replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_b"))
	retained := e.CatalogRead()
	original := retained.MustRelation(parent.Address())
	selected := replicaUnitIndex(t, e, "ri_b")
	root := e.Storage.IndexRoots[selected.Address()]
	store := e.Storage.Relations[parent.Address()]
	var captured bytes.Buffer
	if err := engine.Dump(context.Background(), e, &captured); err != nil {
		t.Fatal(err)
	}
	if retained.MustRelation(parent.Address()) != original || !selected.SQLIndex.ReplicaIdentity || e.Storage.IndexRoots[selected.Address()] != root || e.Storage.Relations[parent.Address()] != store {
		t.Fatal("dump changed captured catalog/storage ownership")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(captured.Bytes())); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "ri_unit").Address)
	currentIndex := replicaUnitIndex(t, e, "ri_b")
	if current.EffectiveReplicaIdentity() != parser.ReplicaIdentityIndex || !currentIndex.SQLIndex.ReplicaIdentity || currentIndex.SQLIndex.Parent != current.Address() || replicaUnitIndex(t, e, "ri_a").SQLIndex.ReplicaIdentity || replicaUnitIndex(t, e, "ri_c").SQLIndex.ReplicaIdentity {
		t.Fatal("replacement remap lost real index selection")
	}
	if retained.MustRelation(parent.Address()) != original || !selected.SQLIndex.ReplicaIdentity || selected.SQLIndex.Parent != parent.Address() {
		t.Fatal("replacement mutated retained immutable capture")
	}
	var redump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &redump); err != nil || redump.String() != captured.String() {
		t.Fatal("noncanonical native row identity", err)
	}
}

func replicaUnitFixture(t *testing.T) (*core.Engine, *session.Session, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	name := enginetest.PreparedBoundaryName("ri_unit")
	enginetest.PreparedUnitStatement(t, e, s, &parser.CreateTable{Name: name, Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "k", TypeName: parser.TypeName{Name: "int4"}, Constraints: []parser.ColumnConstraint{{NotNull: true}}}},
		{Column: &parser.Column{Name: "p", TypeName: parser.TypeName{Name: "text"}}},
	}})
	for _, n := range []string{"ri_a", "ri_b", "ri_c"} {
		enginetest.PreparedUnitStatement(t, e, s, &parser.CreateIndex{Name: parser.Identifier{Text: n}, Table: name, Method: "btree", Unique: true, Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "k"}}}, Include: []parser.IndexKey{{Column: parser.Identifier{Text: "p"}}}})
	}
	enginetest.PreparedUnitStatement(t, e, s, &parser.Insert{Name: name, Rows: [][]parser.Expr{{enginetest.PreparedBoundaryInteger(1), parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "one"}}}}})
	return e, s, e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "ri_unit").Address)
}

func replicaUnitAlter(mode parser.ReplicaIdentityMode, index string) *parser.AlterTable {
	return &parser.AlterTable{Name: enginetest.PreparedBoundaryName("ri_unit"), Actions: []parser.AlterTableAction{{Action: parser.SetReplicaIdentity, ReplicaIdentity: mode, ReplicaIndex: parser.Identifier{Text: index}}}}
}

func replicaUnitIndex(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	a, ok := e.CatalogRead().LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if !ok {
		t.Fatal("missing index", name)
	}
	return e.CatalogRead().MustRelation(a)
}
