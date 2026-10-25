package tablecmds_test

import (
	"context"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestAlterSchemaSourceAuthorityPublicationFence(t *testing.T) {
	e, admin, owner, ns, acl := renameAuthorityFixture(t)
	enginetest.SessionSQL(t, admin, "CREATE SCHEMA s2 AUTHORIZATION rename_transition_owner")
	enginetest.SessionSQL(t, owner, "CREATE SEQUENCE protected.q OWNED BY protected.t.a")
	ref := enginetest.RoleTableRef(t, e, "protected.t")
	q := NameAddressForTempMove(t, e, ns.Address, "q")
	old, sequence, storage := e.Catalog.State.Get(ref.Address), e.Catalog.State.Get(q), e.Storage.Relations[ref.Address]
	stmt := enginetest.Statement(t, "ALTER TABLE protected.t SET SCHEMA s2")
	acl.Entries[0].Privileges = authorization.CreatePrivilege
	var once sync.Once
	var frontier ids.DefinitionRevision
	provider := e.Catalog.Providers[ids.RelationClassID]
	defer func() { e.Catalog.Providers[ids.RelationClassID] = provider }()
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: provider, callback: func() {
		once.Do(func() {
			if err := enginetest.SetObjectACL(e, context.Background(), ns, acl); err != nil {
				t.Fatal(err)
			}
			frontier = e.Catalog.NextRevision()
		})
	}}
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, stmt, 0, nil)
	op.Done()
	e.Catalog.Providers[ids.RelationClassID] = provider
	if err != (catalog.Conflict{}) {
		t.Fatalf("source namespace authorization fence: %T %v", err, err)
	}
	if e.Catalog.State.Get(ref.Address) != old || e.Catalog.State.Get(q) != sequence || e.Storage.Relations[ref.Address] != storage || e.Catalog.NextRevision() != frontier {
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
	enginetest.LockEmpty(t, e)
}

func TestAlterSchemaUnrelatedNamespacePublication(t *testing.T) {
	e := engine.New()
	alterSchemaEngineFixture(t, e)
	provider := e.Catalog.Providers[ids.RelationClassID]
	var once sync.Once
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: provider, callback: func() {
		once.Do(func() { enginetest.Run(t, e, "CREATE SCHEMA unrelated") })
	}}
	enginetest.Run(t, e, "ALTER TABLE s1.t SET SCHEMA s2")
	e.Catalog.Providers[ids.RelationClassID] = provider
	destination := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s2"})
	for _, name := range []string{"t", "q1", "q2"} {
		NameAddressForTempMove(t, e, destination, name)
	}
	if _, ok := e.Catalog.State.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "unrelated"}); !ok {
		t.Fatal("unrelated publication lost")
	}
	enginetest.LockEmpty(t, e)
}

func TestAlterSchemaSourceRenamePublicationFence(t *testing.T) {
	e := engine.New()
	alterSchemaEngineFixture(t, e)
	source := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
	original := make(map[ids.ObjectAddress]catalog.Descriptor)
	for _, name := range []string{"t", "q1", "q2"} {
		a := NameAddressForTempMove(t, e, source, name)
		original[a] = e.Catalog.State.Get(a)
	}
	provider := e.Catalog.Providers[ids.RelationClassID]
	var once sync.Once
	var frontier ids.DefinitionRevision
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: provider, callback: func() {
		once.Do(func() {
			enginetest.Run(t, e, "ALTER SCHEMA s1 RENAME TO renamed_source")
			frontier = e.Catalog.NextRevision()
		})
	}}
	_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "ALTER TABLE s1.t SET SCHEMA s2"), nil)
	e.Catalog.Providers[ids.RelationClassID] = provider
	if err != (catalog.Conflict{}) {
		t.Fatal("source rename fence", err)
	}
	for a, d := range original {
		if e.Catalog.State.Get(a) != d {
			t.Fatal("source rename allowed partial move", a)
		}
	}
	if e.Catalog.NextRevision() != frontier || e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "renamed_source"}) != source {
		t.Fatal("source callback mutation lost")
	}
	enginetest.Run(t, e, "ALTER TABLE renamed_source.t SET SCHEMA s2")
	enginetest.LockEmpty(t, e)
}
