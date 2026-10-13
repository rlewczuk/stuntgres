package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestDropOwnedDefaultCanonicalCleanup(t *testing.T) {
	f := newDefaultUnitFixture(t)
	before := cloneCatalog(f.e.state)
	before.defaultACLs = make(map[defaultACLKey]*defaultACLRecord)
	for class := authorization.DefaultTables; class <= authorization.DefaultLargeObjects; class++ {
		before.defaultACLs[defaultACLKey{creator: f.owner, class: class}] = &defaultACLRecord{acl: authorization.ACLValue{Explicit: true}}
		if class <= authorization.DefaultTypes {
			before.defaultACLs[defaultACLKey{creator: f.owner, schema: f.schema, class: class}] = &defaultACLRecord{acl: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader}, Privileges: authorization.DefaultMask(class)}}}}
		}
	}
	retainedEmpty := defaultACLKey{creator: f.member, class: authorization.DefaultRoutines}
	before.defaultACLs[retainedEmpty] = &defaultACLRecord{acl: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.member, Grantee: authorization.ACLGrantee{Role: f.owner}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege}}}}
	baseline := defaultACLKey{creator: f.member, class: authorization.DefaultTables}
	acl := authorization.DefaultBaseline(f.member, authorization.DefaultTables, false)
	acl.Entries = append(acl.Entries, authorization.ACLEntry{Grantor: f.member, Grantee: authorization.ACLGrantee{Role: f.owner}, Privileges: authorization.SelectPrivilege, GrantOptions: authorization.SelectPrivilege})
	before.defaultACLs[baseline] = &defaultACLRecord{acl: acl}
	untouched := defaultACLKey{creator: f.reader, class: authorization.DefaultTypes}
	before.defaultACLs[untouched] = &defaultACLRecord{acl: authorization.ACLValue{Explicit: true}}
	f.e.state = before
	oldACL := append([]authorization.ACLEntry(nil), before.defaultACLs[baseline].acl.Entries...)
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
	for key := range f.e.state.defaultACLs {
		if key.creator == f.owner || key == baseline {
			t.Fatal("selected creator or baseline policy survived", key)
		}
	}
	empty := f.e.state.defaultACLs[retainedEmpty]
	if empty == nil || !empty.acl.Explicit || len(empty.acl.Entries) != 0 || empty == before.defaultACLs[retainedEmpty] {
		t.Fatal("nonbaseline global empty policy was lost or mutated")
	}
	if f.e.state.defaultACLs[untouched] != before.defaultACLs[untouched] || len(before.defaultACLs) != 13 || !reflect.DeepEqual(before.defaultACLs[baseline].acl.Entries, oldACL) || len(before.defaultACLs[retainedEmpty].acl.Entries) != 1 {
		t.Fatal("cleanup changed retained records or unrelated pointer identity")
	}
}
