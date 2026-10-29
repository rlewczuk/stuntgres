package control

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestPGInitPrivsMetadataBaseline(t *testing.T) {
	source, wrapper, server, _ := fdwUnitSource()
	address := wrapper.Address()
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: source.Bootstrap, Grantee: authorization.ACLGrantee{Role: source.Bootstrap}, Privileges: authorization.UsagePrivilege},
		{Grantor: source.Bootstrap, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
	}}
	record := &catalog.InitialPrivilegeRecord{PrivType: 'i', ACL: acl}
	source.State.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{address: record})
	source.State.SetInitialPrivilegeBaseline(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{address: record})
	if !InitialPrivilegesMatchBaseline(source.CatalogView) {
		t.Fatal("untouched canonical seed requires metadata")
	}
	if metadata, err := CaptureInitialPrivileges(context.Background(), source.CatalogView, source.OriginalRoles); err != nil || metadata != nil {
		t.Fatal("canonical history emitted metadata", metadata, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CaptureInitialPrivileges(cancelled, source.CatalogView, source.OriginalRoles); err != context.Canceled {
		t.Fatal("canonical omission bypassed cancellation", err)
	}
	invalid := catalog.CloneState(source.State)
	foreign := *record
	foreign.ACL.Entries = append([]authorization.ACLEntry(nil), record.ACL.Entries...)
	foreign.ACL.Entries[0].Grantor = new(authorization.Domain).Principal(source.Bootstrap.ID())
	invalid.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{address: &foreign})
	invalid.SetInitialPrivilegeBaseline(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{address: &foreign})
	if _, err := CaptureInitialPrivileges(context.Background(), source.CatalogView.WithState(invalid), source.OriginalRoles); err == nil {
		t.Fatal("canonical omission bypassed current principal validation")
	}
	for _, change := range []string{"absence", "origin", "entry order", "grant options", "additional history", "principal authority"} {
		t.Run(change, func(t *testing.T) {
			next := catalog.CloneState(source.State)
			copy := *record
			copy.ACL.Entries = append([]authorization.ACLEntry(nil), record.ACL.Entries...)
			records := map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{address: &copy}
			switch change {
			case "absence":
				delete(records, address)
			case "origin":
				copy.PrivType = 'e'
			case "entry order":
				copy.ACL.Entries[0], copy.ACL.Entries[1] = copy.ACL.Entries[1], copy.ACL.Entries[0]
			case "grant options":
				copy.ACL.Entries[0].GrantOptions = authorization.UsagePrivilege
			case "additional history":
				records[server.Address()] = record
			case "principal authority":
				copy.ACL.Entries[0].Grantor = new(authorization.Domain).Principal(source.Bootstrap.ID())
			}
			next.SetInitialPrivileges(records)
			if InitialPrivilegesMatchBaseline(source.CatalogView.WithState(next)) {
				t.Fatal("historical change incorrectly omitted metadata")
			}
			if !InitialPrivilegesMatchBaseline(source.CatalogView) {
				t.Fatal("comparison baseline mutated")
			}
		})
	}
	// Metadata exclusion follows TEMP portability, not total inventory size.
	temporary := catalog.CloneState(source.State)
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 950200}, ObjectName: "pg_temp_baseline", ObjectRevision: 1, Owner: source.Bootstrap, Temporary: new(ids.Domain).Session(1)}
	temporary.SetDescriptor(ns.Address(), ns)
	temporary.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{address: record, ns.Address(): record})
	if !InitialPrivilegesMatchBaseline(source.CatalogView.WithState(temporary)) {
		t.Fatal("TEMP-only historical change affected persistent dump")
	}
	if !temporaryDumpObject(temporary, ns) {
		t.Fatal("TEMP namespace itself is dump-visible")
	}
	if metadata, err := CaptureInitialPrivileges(context.Background(), source.CatalogView.WithState(temporary), source.OriginalRoles); err != nil || metadata != nil {
		t.Fatal("TEMP namespace history escaped canonical dump omission", metadata, err)
	}
	changed := catalog.CloneState(temporary)
	changed.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{address: record, server.Address(): record, ns.Address(): record})
	metadata, err := CaptureInitialPrivileges(context.Background(), source.CatalogView.WithState(changed), source.OriginalRoles)
	if err != nil || metadata == nil || len(metadata.Records) != 2 {
		t.Fatal("TEMP namespace history changed complete persistent inventory", metadata, err)
	}
	for _, entry := range metadata.Records {
		if entry.Name == ns.Name() {
			t.Fatal("TEMP namespace leaked into complete historical metadata")
		}
	}
	// Equal values in distinct immutable records do not imply a history change.
	equal := catalog.CloneState(source.State)
	copy := *record
	copy.ACL.Entries = append([]authorization.ACLEntry(nil), record.ACL.Entries...)
	equal.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{address: &copy})
	if !InitialPrivilegesMatchBaseline(source.CatalogView.WithState(equal)) {
		t.Fatal("comparison used record/backing identity instead of exact ACL value")
	}
	// Missing baseline objects are not admitted as live records, but remain
	// necessary comparison facts after a supported seed object was dropped.
	absent := catalog.CloneState(source.State)
	absent.RemoveDescriptor(address)
	absent.SetInitialPrivileges(nil)
	if InitialPrivilegesMatchBaseline(source.CatalogView.WithState(absent)) {
		t.Fatal("absent bootstrap history omitted complete metadata")
	}
	if err := remapInitialPrivilegeAddresses(absent, map[ids.ObjectAddress]ids.ObjectAddress{address: {ClassID: address.ClassID, ObjectID: address.ObjectID + 100}}); err != nil {
		t.Fatal("baseline remap incorrectly required retired object liveness", err)
	}
}
