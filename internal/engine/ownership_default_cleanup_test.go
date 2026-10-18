package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestDropOwnedDefaultCanonicalCleanup(t *testing.T) {
	f := newDefaultUnitFixture(t)
	before := cloneCatalog(f.e.catalog.state)
	before.defaultACLs = make(map[defaultACLKey]*defaultACLRecord)
	for class := authorization.DefaultTables; class <= authorization.DefaultLargeObjects; class++ {
		before.defaultACLs[defaultACLKey{Creator: f.owner, Class: class}] = &defaultACLRecord{ACL: authorization.ACLValue{Explicit: true}}
		if class <= authorization.DefaultTypes {
			before.defaultACLs[defaultACLKey{Creator: f.owner, Schema: f.schema, Class: class}] = &defaultACLRecord{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader}, Privileges: authorization.DefaultMask(class)}}}}
		}
	}
	retainedEmpty := defaultACLKey{Creator: f.member, Class: authorization.DefaultRoutines}
	before.defaultACLs[retainedEmpty] = &defaultACLRecord{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.member, Grantee: authorization.ACLGrantee{Role: f.owner}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege}}}}
	baseline := defaultACLKey{Creator: f.member, Class: authorization.DefaultTables}
	acl := authorization.DefaultBaseline(f.member, authorization.DefaultTables, false)
	acl.Entries = append(acl.Entries, authorization.ACLEntry{Grantor: f.member, Grantee: authorization.ACLGrantee{Role: f.owner}, Privileges: authorization.SelectPrivilege, GrantOptions: authorization.SelectPrivilege})
	before.defaultACLs[baseline] = &defaultACLRecord{ACL: acl}
	untouched := defaultACLKey{Creator: f.reader, Class: authorization.DefaultTypes}
	before.defaultACLs[untouched] = &defaultACLRecord{ACL: authorization.ACLValue{Explicit: true}}
	f.e.catalog.state = before
	oldACL := append([]authorization.ACLEntry(nil), before.defaultACLs[baseline].ACL.Entries...)
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	defer f.e.DiscardCatalogStage(stage)
	stage.roleRevision = f.e.roles.revision
	// Namespace AUTO tombstones precede cleanup and must not be resurrected.
	stage.removeSchemaDefaults(f.schema)
	if err := stageOwnedCleanup(context.Background(), stage, f.e.roleRead(stage.base, f.e.roles), f.e.roles, map[authorization.PrincipalRef]bool{f.owner: true}); err != nil {
		t.Fatal(err)
	}
	if !ownedCleanupAddsDeletionRoot(stage, map[authorization.PrincipalRef]bool{f.owner: true}) {
		t.Fatal("actual creator records were not collected")
	}
	if err := defaultUnitPublish(t, f.e, stage); err != nil {
		t.Fatal(err)
	}
	for key := range f.e.catalog.state.defaultACLs {
		if key.Creator == f.owner || key == baseline {
			t.Fatal("selected creator or baseline policy survived", key)
		}
	}
	empty := f.e.catalog.state.defaultACLs[retainedEmpty]
	if empty == nil || !empty.ACL.Explicit || len(empty.ACL.Entries) != 0 || empty == before.defaultACLs[retainedEmpty] {
		t.Fatal("nonbaseline global empty policy was lost or mutated")
	}
	if f.e.catalog.state.defaultACLs[untouched] != before.defaultACLs[untouched] || len(before.defaultACLs) != 13 || !reflect.DeepEqual(before.defaultACLs[baseline].ACL.Entries, oldACL) || len(before.defaultACLs[retainedEmpty].ACL.Entries) != 1 {
		t.Fatal("cleanup changed retained records or unrelated pointer identity")
	}
}
