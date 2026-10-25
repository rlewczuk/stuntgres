package engine

import (
	"context"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTableLikeIndexClone(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE src(dead TEXT,a INT UNIQUE NULLS NOT DISTINCT,b TEXT)")
	run(t, e, "ALTER TABLE src DROP COLUMN dead")
	run(t, e, `CREATE INDEX covering ON src(b COLLATE "C" text_pattern_ops DESC NULLS LAST) INCLUDE(a)`)
	run(t, e, "CREATE INDEX derived ON src((a+1))")
	run(t, e, "CREATE INDEX hashed ON src USING hash(a)")
	sourceRoots := e.Storage.IndexRoots
	op, done := roleOperation(t, e, "postgres")
	defer done()
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(head BOOL,lead TEXT,LIKE src INCLUDING INDEXES)")
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	count := 0
	for _, descriptor := range stage.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		count++
		definition := index.SQLIndex
		if definition.Parent != destination.Address() || index.Owner != destination.Owner || !definition.Ready || !definition.Valid || !definition.Live || index.Security.ACL.Explicit {
			t.Fatal(index)
		}
		switch index.Name() {
		case "clone_a_key":
			if definition.Keys[0].Attribute != 3 || !definition.Unique || !definition.NullsNotDistinct || definition.Constraint == nil || definition.Constraint.OID == 0 || definition.Constraint.Index != index.Address() || definition.Constraint.Name != index.Name() || len(destination.Constraints.Unique) != 1 || destination.Constraints.Unique[0].OID != definition.Constraint.OID {
				t.Fatal("backing constraint mismatch", index)
			}
		case "clone_b_a_idx":
			key := definition.Keys[0]
			if key.Attribute != 4 || !key.Desc || key.NullsFirst || key.Class.Name != "text_pattern_ops" || key.Collation != 950 || !slices.Equal(definition.Include, []int32{3}) {
				t.Fatal("covering key attributes lost", definition)
			}
		case "clone_expr_idx":
			key := definition.Keys[0].Derived
			if key == nil || !slices.Equal(key.Attributes, destination.Attributes) {
				t.Fatal("derived layout lost", definition)
			}
			for _, ref := range key.Dependencies {
				if ref.Address.ClassID == ids.RelationClassID && ref.Address.ObjectID != destination.Address().ObjectID {
					t.Fatal("derived source row dependency", ref)
				}
			}
		case "clone_a_idx":
			if definition.Method != catalog.HashMethod || definition.Keys[0].Attribute != 3 || definition.Keys[0].Class.Name != "int4_ops" {
				t.Fatal("hash definition lost", definition)
			}
		default:
			t.Fatal("source spelling retained", index.Name())
		}
	}
	if count != 4 {
		t.Fatal("missing copied indexes", count)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range stage.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if ok && index.SQLIndex != nil && e.Storage.IndexRoots[index.Address()] == nil {
			t.Fatal("new index has no actual published root", index.Name())
		}
		if ok && index.SQLIndex != nil {
			for address, root := range sourceRoots {
				if e.Storage.IndexRoots[address] != root || e.Storage.IndexRoots[index.Address()] == root {
					t.Fatal("copied index reused or changed a source root", index.Name())
				}
			}
		}
	}
}

func TestTableLikeInvalidIndexDescriptors(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE src(a INT)")
	run(t, e, "CREATE UNIQUE INDEX invalid_source ON src(a)")
	run(t, e, "CREATE INDEX dropping_source ON src(a)")
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	candidate := catalog.CloneState(auth.Catalog.State())
	for _, name := range []string{"invalid_source", "dropping_source"} {
		ref := ids.ObjectRef{Address: candidate.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})}
		original := candidate.MustRelation(ref.Address)
		copy := *original
		definition := *original.SQLIndex
		definition.Ready, definition.Valid = false, false
		if name == "dropping_source" {
			definition.Live = false
		}
		copy.SQLIndex = &definition
		candidate.SetDescriptor(ref.Address, &copy)
	}
	auth.Catalog = e.Catalog.Read(candidate)
	auth.Recapture(auth.Catalog)
	stage := e.BeginCatalogStage(auth.Catalog)
	syntax := statement(t, "CREATE TABLE clone(LIKE src INCLUDING INDEXES)").(*parser.CreateTable)
	err = e.SchemaTableLocked(op, auth, stage, syntax, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	count := 0
	for _, descriptor := range stage.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		count++
		if index.Name() != "clone_a_idx" || !index.SQLIndex.Unique || !index.SQLIndex.Ready || !index.SQLIndex.Valid || !index.SQLIndex.Live {
			t.Fatal("invalid source definition not rebuilt", index)
		}
	}
	if count != 1 {
		t.Fatal("nonlive copied or live invalid omitted", count)
	}
}
