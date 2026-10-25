package rolecmds_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
)

func TestDropOwnedDefaultCanonicalCleanup(t *testing.T) {
	f := newDefaultUnitFixture(t)
	before := catalog.CloneState(f.e.Catalog.State)
	before.SetDefaultACLs(make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord))
	for class := authorization.DefaultTables; class <= authorization.DefaultLargeObjects; class++ {
		before.SetDefaultACL(catalog.DefaultACLKey{Creator: f.owner, Class: class}, &catalog.DefaultACLRecord{OID: 20000 + ids.OID(class), ACL: authorization.ACLValue{Explicit: true}})
		if class <= authorization.DefaultTypes {
			before.SetDefaultACL(catalog.DefaultACLKey{Creator: f.owner, Schema: f.schema, Class: class}, &catalog.DefaultACLRecord{OID: 20100 + ids.OID(class), ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader}, Privileges: authorization.DefaultMask(class)}}}})
		}
	}
	retainedEmpty := catalog.DefaultACLKey{Creator: f.member, Class: authorization.DefaultRoutines}
	before.SetDefaultACL(retainedEmpty, &catalog.DefaultACLRecord{OID: 20200, ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.member, Grantee: authorization.ACLGrantee{Role: f.owner}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege}}}})
	baseline := catalog.DefaultACLKey{Creator: f.member, Class: authorization.DefaultTables}
	acl := authorization.DefaultBaseline(f.member, authorization.DefaultTables, false)
	acl.Entries = append(acl.Entries, authorization.ACLEntry{Grantor: f.member, Grantee: authorization.ACLGrantee{Role: f.owner}, Privileges: authorization.SelectPrivilege, GrantOptions: authorization.SelectPrivilege})
	before.SetDefaultACL(baseline, &catalog.DefaultACLRecord{OID: 20201, ACL: acl})
	untouched := catalog.DefaultACLKey{Creator: f.reader, Class: authorization.DefaultTypes}
	before.SetDefaultACL(untouched, &catalog.DefaultACLRecord{OID: 20202, ACL: authorization.ACLValue{Explicit: true}})
	f.e.Catalog.PublishLocked(before)
	oldACL := append([]authorization.ACLEntry(nil), before.DefaultACL(baseline).ACL.Entries...)
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	defer f.e.DiscardCatalogStage(stage)
	stage.RoleRevision = f.e.RoleStore.Published.Revision
	// Namespace AUTO tombstones precede cleanup and must not be resurrected.
	stage.RemoveSchemaDefaults(f.schema)
	if err := role.StageOwnedCleanup(context.Background(), stage, f.e.RoleStore.PublishedRead(&f.e.Catalog.Owner, stage.Base), f.e.RoleStore.Published, map[authorization.PrincipalRef]bool{f.owner: true}); err != nil {
		t.Fatal(err)
	}
	if !role.OwnedCleanupAddsDeletionRoot(stage, map[authorization.PrincipalRef]bool{f.owner: true}) {
		t.Fatal("actual creator records were not collected")
	}
	if err := defaultUnitPublish(t, f.e, stage); err != nil {
		t.Fatal(err)
	}
	for key := range f.e.Catalog.State.DefaultACLs() {
		if key.Creator == f.owner || key == baseline {
			t.Fatal("selected creator or baseline policy survived", key)
		}
	}
	empty := f.e.Catalog.State.DefaultACL(retainedEmpty)
	if empty == nil || empty.OID != before.DefaultACL(retainedEmpty).OID || !empty.ACL.Explicit || len(empty.ACL.Entries) != 0 || empty == before.DefaultACL(retainedEmpty) {
		t.Fatal("nonbaseline global empty policy was lost or mutated")
	}
	if f.e.Catalog.State.DefaultACL(untouched) != before.DefaultACL(untouched) || before.DefaultACLCount() != 13 || !reflect.DeepEqual(before.DefaultACL(baseline).ACL.Entries, oldACL) || len(before.DefaultACL(retainedEmpty).ACL.Entries) != 1 {
		t.Fatal("cleanup changed retained records or unrelated pointer identity")
	}
}

func defaultUnitPublish(t *testing.T, e *engine.Engine, stage *engine.CatalogStage) error {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		return err
	}
	_, err = enginetest.PublishCatalog(e, p, engine.PublicationToken{Epoch: stage.Base.Epoch()})
	return err
}
