package control

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestPGInitPrivsRemapAddresses(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 950001}
	b := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 950002}
	for _, address := range []ids.ObjectAddress{a, b} {
		source.State.SetDescriptor(address, &catalog.Relation{ObjectAddress: address, ObjectName: "columns", ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{1, 3}})
	}
	columnA, columnB := a, b
	columnA.SubID, columnB.SubID = 3, 1
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: source.Bootstrap, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
	first := &catalog.InitialPrivilegeRecord{PrivType: 'e', ACL: acl}
	second := &catalog.InitialPrivilegeRecord{PrivType: 'i', ACL: acl}
	source.State.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{columnA: first, columnB: second})
	next := catalog.CloneState(source.State)
	if err := remapInitialPrivilegeAddresses(next, map[ids.ObjectAddress]ids.ObjectAddress{a: b, b: a}); err != nil {
		t.Fatal(err)
	}
	wantFirst, wantSecond := b, a
	wantFirst.SubID, wantSecond.SubID = 3, 1
	if next.InitialPrivilege(wantFirst) != first || next.InitialPrivilege(wantSecond) != second || source.State.InitialPrivilege(columnA) != first {
		t.Fatal("cyclic remap lost stable subIDs or mutated source")
	}
	// A dropped column must not silently retarget the same slice position.
	dropped := catalog.CloneState(source.State)
	target := *dropped.Get(b).(*catalog.Relation)
	target.Attributes = []int32{1}
	dropped.SetDescriptor(b, &target)
	if err := remapInitialPrivilegeAddresses(dropped, map[ids.ObjectAddress]ids.ObjectAddress{a: b, b: a}); err == nil || dropped.InitialPrivilege(columnA) != first {
		t.Fatal("dropped subID accepted or failed mapping changed history")
	}
	duplicate := catalog.CloneState(source.State)
	duplicate.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{a: first, b: second})
	if err := remapInitialPrivilegeAddresses(duplicate, map[ids.ObjectAddress]ids.ObjectAddress{a: b}); err == nil || duplicate.InitialPrivilege(a) != first || duplicate.InitialPrivilege(b) != second {
		t.Fatal("duplicate destination accepted or source map changed")
	}
}

func TestPGInitPrivsRemapTemporary(t *testing.T) {
	source, wrapper, _, _ := fdwUnitSource()
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 950100}, ObjectName: "pg_temp_history", ObjectRevision: 1, Owner: source.Bootstrap, Temporary: new(ids.Domain).Session(1)}
	source.State.SetDescriptor(namespace.Address(), namespace)
	source.State.BindName(catalog.DescriptorNameKey(namespace), namespace.Address())
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: source.Bootstrap, Grantee: authorization.ACLGrantee{Role: source.Bootstrap}, Privileges: authorization.UsagePrivilege}}}
	temporaryRecord := &catalog.InitialPrivilegeRecord{PrivType: 'e', ACL: acl}
	persistentRecord := &catalog.InitialPrivilegeRecord{PrivType: 'i', ACL: acl}
	source.State.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{namespace.Address(): temporaryRecord, wrapper.Address(): persistentRecord})
	portable, err := PersistentReplacementCatalog(source.CatalogView)
	if err != nil {
		t.Fatal(err)
	}
	if portable.State().InitialPrivilege(namespace.Address()) != nil || portable.State().InitialPrivilege(wrapper.Address()) != persistentRecord || source.State.InitialPrivilege(namespace.Address()) != temporaryRecord {
		t.Fatal("source TEMP history pruning changed wrong root")
	}
	next := catalog.CloneState(portable.State())
	next.SetDescriptor(namespace.Address(), namespace)
	if err := RetainTemporaryInitialPrivileges(source.CatalogView, next, map[ids.ObjectAddress]catalog.Descriptor{namespace.Address(): namespace}); err != nil {
		t.Fatal(err)
	}
	if next.InitialPrivilege(namespace.Address()) != temporaryRecord || portable.State().InitialPrivilege(namespace.Address()) != nil {
		t.Fatal("destination TEMP history not retained immutably")
	}
	if err := RetainTemporaryInitialPrivileges(source.CatalogView, next, map[ids.ObjectAddress]catalog.Descriptor{namespace.Address(): namespace}); err == nil {
		t.Fatal("conflicting imported history accepted at TEMP address")
	}
}
