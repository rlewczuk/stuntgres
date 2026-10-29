package core_test

import (
	"bytes"
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"testing"
)

func TestReplicaIdentityNativeReplay(t *testing.T) {
	e, s, parent := replicaUnitFixture(t)
	preparedUnitStatement(t, e, s, replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_b"))
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
