package engine

import (
	"context"
	"slices"
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
	sourceRoots := e.indexRoots
	op, done := roleOperation(t, e, "postgres")
	defer done()
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(head BOOL,lead TEXT,LIKE src INCLUDING INDEXES)")
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	count := 0
	for _, descriptor := range stage.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.SQLIndex == nil {
			continue
		}
		count++
		definition := index.SQLIndex
		if definition.parent != destination.address || index.Owner != destination.Owner || !definition.ready || !definition.valid || !definition.live || index.ACL.Explicit {
			t.Fatal(index)
		}
		switch index.name {
		case "clone_a_key":
			if definition.keys[0].attribute != 3 || !definition.unique || !definition.nullsNotDistinct || definition.constraint == nil || definition.constraint.OID == 0 || definition.constraint.Index != index.address || definition.constraint.Name != index.name || len(destination.UniqueConstraints) != 1 || destination.UniqueConstraints[0].OID != definition.constraint.OID {
				t.Fatal("backing constraint mismatch", index)
			}
		case "clone_b_a_idx":
			key := definition.keys[0]
			if key.attribute != 4 || !key.desc || key.nullsFirst || key.class.Name != "text_pattern_ops" || key.collation != 950 || !slices.Equal(definition.include, []int32{3}) {
				t.Fatal("covering key attributes lost", definition)
			}
		case "clone_expr_idx":
			key := definition.keys[0].derived
			if key == nil || !slices.Equal(key.attributes, destination.Attributes) {
				t.Fatal("derived layout lost", definition)
			}
			for _, ref := range key.dependencies {
				if ref.Address.ClassID == ids.RelationClassID && ref.Address.ObjectID != destination.address.ObjectID {
					t.Fatal("derived source row dependency", ref)
				}
			}
		case "clone_a_idx":
			if definition.method != hashMethod || definition.keys[0].attribute != 3 || definition.keys[0].class.Name != "int4_ops" {
				t.Fatal("hash definition lost", definition)
			}
		default:
			t.Fatal("source spelling retained", index.name)
		}
	}
	if count != 4 {
		t.Fatal("missing copied indexes", count)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range stage.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if ok && index.SQLIndex != nil && e.indexRoots[index.address] == nil {
			t.Fatal("new index has no actual published root", index.name)
		}
		if ok && index.SQLIndex != nil {
			for address, root := range sourceRoots {
				if e.indexRoots[address] != root || e.indexRoots[index.address] == root {
					t.Fatal("copied index reused or changed a source root", index.name)
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
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	candidate := cloneCatalog(auth.catalog.state)
	for _, name := range []string{"invalid_source", "dropping_source"} {
		ref := ids.ObjectRef{Address: candidate.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})}
		original := candidate.MustRelation(ref.Address)
		copy := *original
		definition := *original.SQLIndex
		definition.ready, definition.valid = false, false
		if name == "dropping_source" {
			definition.live = false
		}
		copy.SQLIndex = &definition
		candidate.setDescriptor(ref.Address, &copy)
	}
	auth.catalog = e.catalog.read(candidate)
	auth.recaptureCatalog(auth.catalog)
	stage := e.BeginCatalogStage(auth.catalog)
	syntax := statement(t, "CREATE TABLE clone(LIKE src INCLUDING INDEXES)").(*parser.CreateTable)
	err = e.schemaTableLocked(op, auth, stage, syntax, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.state.CurrentUser, sqltype.InputEnvironment{}, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	count := 0
	for _, descriptor := range stage.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.SQLIndex == nil {
			continue
		}
		count++
		if index.name != "clone_a_idx" || !index.SQLIndex.unique || !index.SQLIndex.ready || !index.SQLIndex.valid || !index.SQLIndex.live {
			t.Fatal("invalid source definition not rebuilt", index)
		}
	}
	if count != 1 {
		t.Fatal("nonlive copied or live invalid omitted", count)
	}
}
