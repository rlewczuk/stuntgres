package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"sync"
	"testing"
)

func TestAlterSchemaSourceAuthorityPublicationFence(t *testing.T) {
	e, admin, owner, ns, acl := renameAuthorityFixture(t)
	sessionSQL(t, admin, "CREATE SCHEMA s2 AUTHORIZATION rename_transition_owner")
	sessionSQL(t, owner, "CREATE SEQUENCE protected.q OWNED BY protected.t.a")
	ref := roleTableRef(t, e, "protected.t")
	q := NameAddressForTempMove(t, e, ns.Address, "q")
	old, sequence, storage := e.state.descriptors[ref.Address], e.state.descriptors[q], e.storage[ref.Address]
	stmt := statement(t, "ALTER TABLE protected.t SET SCHEMA s2")
	acl.Entries[0].Privileges = authorization.CreatePrivilege
	var once sync.Once
	var frontier ids.DefinitionRevision
	provider := e.providers[RelationClassID]
	defer func() { e.providers[RelationClassID] = provider }()
	e.providers[RelationClassID] = alterDropProvider{CatalogProvider: provider, callback: func() {
		once.Do(func() {
			if err := e.SetObjectACL(context.Background(), ns, acl); err != nil {
				t.Fatal(err)
			}
			frontier = e.nextRevision
		})
	}}
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, stmt, 0, nil)
	op.Done()
	e.providers[RelationClassID] = provider
	if err != (CatalogConflict{}) {
		t.Fatalf("source namespace authorization fence: %T %v", err, err)
	}
	if e.state.descriptors[ref.Address] != old || e.state.descriptors[q] != sequence || e.storage[ref.Address] != storage || e.nextRevision != frontier {
		t.Fatal("source revocation allowed partial publication")
	}
	op, err = owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, stmt, 0, nil)
	op.Done()
	renameWantError(t, err, "42501", "permission denied for schema protected")
	lockEmpty(t, e)
}

func TestAlterSchemaUnrelatedNamespacePublication(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	provider := e.providers[RelationClassID]
	var once sync.Once
	e.providers[RelationClassID] = alterDropProvider{CatalogProvider: provider, callback: func() {
		once.Do(func() { run(t, e, "CREATE SCHEMA unrelated") })
	}}
	run(t, e, "ALTER TABLE s1.t SET SCHEMA s2")
	e.providers[RelationClassID] = provider
	destination := e.state.names[ids.NameKey{ClassID: NamespaceClassID, Name: "s2"}]
	for _, name := range []string{"t", "q1", "q2"} {
		NameAddressForTempMove(t, e, destination, name)
	}
	if _, ok := e.state.names[ids.NameKey{ClassID: NamespaceClassID, Name: "unrelated"}]; !ok {
		t.Fatal("unrelated publication lost")
	}
	lockEmpty(t, e)
}

func TestAlterSchemaSourceRenamePublicationFence(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	source := e.state.names[ids.NameKey{ClassID: NamespaceClassID, Name: "s1"}]
	original := make(map[ids.ObjectAddress]Descriptor)
	for _, name := range []string{"t", "q1", "q2"} {
		a := NameAddressForTempMove(t, e, source, name)
		original[a] = e.state.descriptors[a]
	}
	provider := e.providers[RelationClassID]
	var once sync.Once
	var frontier ids.DefinitionRevision
	e.providers[RelationClassID] = alterDropProvider{CatalogProvider: provider, callback: func() {
		once.Do(func() {
			run(t, e, "ALTER SCHEMA s1 RENAME TO renamed_source")
			frontier = e.nextRevision
		})
	}}
	_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE s1.t SET SCHEMA s2"), nil)
	e.providers[RelationClassID] = provider
	if err != (CatalogConflict{}) {
		t.Fatal("source rename fence", err)
	}
	for a, d := range original {
		if e.state.descriptors[a] != d {
			t.Fatal("source rename allowed partial move", a)
		}
	}
	if e.nextRevision != frontier || e.state.names[ids.NameKey{ClassID: NamespaceClassID, Name: "renamed_source"}] != source {
		t.Fatal("source callback mutation lost")
	}
	run(t, e, "ALTER TABLE renamed_source.t SET SCHEMA s2")
	lockEmpty(t, e)
}
