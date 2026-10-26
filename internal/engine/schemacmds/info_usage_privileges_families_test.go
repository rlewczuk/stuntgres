package schemacmds_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestInfoUsagePrivilegesFamilies(t *testing.T) {
	f := newUsagePrivilegesFixture(t)
	root := f.f.e.Roles().LookupRef("postgres")
	sequence := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20540}, ObjectNamespace: f.f.schema, ObjectName: "usage_sequence", ObjectRevision: 1, RelationKind: catalog.SequenceRelation, Owner: f.f.owner}
	wrapper := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignWrapperClassID, ObjectID: 20541}, ObjectRevision: 1, ObjectName: "usage_wrapper", Owner: f.f.owner}
	server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 20542}, ObjectRevision: 1, ObjectName: "usage_server", Owner: f.f.owner, Wrapper: wrapper.Address()}
	collation := &catalog.Collation{OID: 20543, ObjectName: "usage_collation", ObjectNamespace: f.f.schema, ObjectRevision: 1, Encoding: -1, Owner: f.f.owner}
	for _, object := range []catalog.Descriptor{sequence, wrapper, server, collation} {
		f.state.SetDescriptor(object.Address(), object)
	}
	check := func(actor authorization.PrincipalRef, name, schema, family string, origins ...[3]string) {
		t.Helper()
		want := make(map[[8]string]int)
		for _, origin := range origins {
			want[[8]string{origin[0], origin[1], "unit_database", schema, name, family, "USAGE", origin[2]}]++
		}
		got := usagePrivilegesBag(t, usagePrivilegesRows(t, f.input(t, f.state, actor)), name)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s grants=%v want=%v", name, got, want)
		}
	}
	owner := [3]string{"default_owner", "default_owner", "YES"}
	reader := [3]string{"default_owner", "default_reader", "NO"}
	public := [3]string{"default_owner", "PUBLIC", "NO"}
	check(f.f.owner, sequence.Name(), "default_schema", "SEQUENCE")
	check(root, sequence.Name(), "default_schema", "SEQUENCE")
	check(f.f.owner, wrapper.Name(), "", "FOREIGN DATA WRAPPER", owner)
	check(f.f.reader, wrapper.Name(), "", "FOREIGN DATA WRAPPER")
	check(f.f.owner, server.Name(), "", "FOREIGN SERVER", owner)
	check(f.f.owner, collation.Name(), "default_schema", "COLLATION", public)
	check(f.f.reader, collation.Name(), "default_schema", "COLLATION", public)
	for _, encoding := range []int32{6, 8, -1} {
		copy := *collation
		copy.Encoding = encoding
		f.state.SetDescriptor(copy.Address(), &copy)
		if encoding == 8 {
			check(root, copy.Name(), "default_schema", "COLLATION")
		} else {
			check(f.f.owner, copy.Name(), "default_schema", "COLLATION", public)
		}
	}
	for _, family := range []string{"SEQUENCE", "FOREIGN DATA WRAPPER", "FOREIGN SERVER"} {
		for _, explicit := range []bool{false, true} {
			entries := []authorization.ACLEntry{
				{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Role: f.f.owner}, Privileges: authorization.UsagePrivilege},
				defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0),
				defaultUnitReaderEntry(f.f, authorization.SelectPrivilege|authorization.UpdatePrivilege, 0),
			}
			acl := authorization.ACLValue{Explicit: true}
			if explicit {
				acl.Entries = entries
			}
			name, schema := sequence.Name(), "default_schema"
			switch family {
			case "SEQUENCE":
				copy := *sequence
				copy.Security.ACL = acl
				f.state.SetDescriptor(copy.Address(), &copy)
			case "FOREIGN DATA WRAPPER":
				copy := *wrapper
				copy.ACL = acl
				f.state.SetDescriptor(copy.Address(), &copy)
				name, schema = copy.Name(), ""
			case "FOREIGN SERVER":
				copy := *server
				copy.ACL = acl
				f.state.SetDescriptor(copy.Address(), &copy)
				name, schema = copy.Name(), ""
			}
			if explicit {
				check(f.f.owner, name, schema, family, owner, reader)
				check(f.f.reader, name, schema, family, reader)
			} else {
				check(root, name, schema, family)
			}
		}
	}
	// Raw synthetic type defaults must not masquerade as a materialized ACL.
	domain := *f.udt
	domain.ACLSyntheticDefault = true
	domain.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)}}
	f.state.SetDescriptor(domain.Address(), &domain)
	f.check(t, f.state, f.f.owner,
		usagePrivilegesWant("default_owner", "default_owner", "USAGE", "YES"),
		usagePrivilegesWant("default_owner", "PUBLIC", "USAGE", "NO"))
	f.check(t, f.state, f.f.reader, usagePrivilegesWant("default_owner", "PUBLIC", "USAGE", "NO"))
}
