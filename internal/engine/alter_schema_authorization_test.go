package engine

import (
	"context"
	"testing"
)

func TestAlterSchemaAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, actor, destination, code, message string
		source, target                          Privileges
	}{
		{"source_usage_only", "move_owner", "s2", "", "", UsagePrivilege, UsagePrivilege | CreatePrivilege},
		{"destination_create_only", "move_owner", "s2", "", "", UsagePrivilege, CreatePrivilege},
		{"destination_usage_only", "move_owner", "s2", "42501", "permission denied for schema s2", UsagePrivilege, UsagePrivilege},
		{"destination_neither", "move_owner", "s2", "42501", "permission denied for schema s2", UsagePrivilege, 0},
		{"source_no_usage", "move_owner", "s2", "42501", "permission denied for schema s1", CreatePrivilege, CreatePrivilege},
		{"same_schema_no_create", "move_owner", "s1", "42501", "permission denied for schema s1", UsagePrivilege, CreatePrivilege},
		{"schema_owner_no_source_usage", "move_schema_owner", "s2", "42501", "permission denied for schema s1", 0, CreatePrivilege},
		{"schema_owner_not_table_owner", "move_schema_owner", "s2", "42501", "must be owner of table t", UsagePrivilege, CreatePrivilege},
		{"nonowner_with_table_rights", "move_schema_owner", "s2", "42501", "must be owner of table t", UsagePrivilege, CreatePrivilege},
		{"superuser", "postgres", "s2", "", "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE USER move_owner", "CREATE USER move_schema_owner", "CREATE SCHEMA s1 AUTHORIZATION move_schema_owner", "CREATE SCHEMA s2 AUTHORIZATION move_schema_owner"} {
				roleExecute(t, e, op, q)
			}
			owner, _ := e.Roles().Lookup("move_owner")
			schemaOwner, _ := e.Roles().Lookup("move_schema_owner")
			set := func(name string, grantee PrincipalRef, privileges Privileges) {
				t.Helper()
				a := e.state.names[NameKey{ClassID: NamespaceClassID, Name: name}]
				acl := ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: schemaOwner.Ref, Grantee: ACLGrantee{Role: grantee}, Privileges: privileges}}}
				if err := e.SetObjectACL(context.Background(), ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: a}, acl); err != nil {
					t.Fatal(err)
				}
			}
			set("s1", owner.Ref, UsagePrivilege|CreatePrivilege)
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION move_owner")
			for _, q := range []string{"CREATE TABLE s1.t(id INT)", "INSERT INTO s1.t VALUES(7)", "CREATE SEQUENCE s1.q OWNED BY s1.t.id"} {
				roleExecute(t, e, op, q)
			}
			roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
			actor, ok := e.Roles().Lookup(tc.actor)
			if !ok {
				t.Fatal("missing actor", tc.actor)
			}
			set("s1", actor.Ref, tc.source)
			set("s2", actor.Ref, tc.target)
			source := e.state.names[NameKey{ClassID: NamespaceClassID, Name: "s1"}]
			a := NameAddressForTempMove(t, e, source, "t")
			q := NameAddressForTempMove(t, e, source, "q")
			if tc.name == "nonowner_with_table_rights" {
				table := e.state.descriptors[a].(*relationDescriptor)
				acl := ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: table.owner, Grantee: ACLGrantee{Role: schemaOwner.Ref}, Privileges: SelectPrivilege}}}
				if err := e.SetObjectACL(context.Background(), ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: a}, acl); err != nil {
					t.Fatal(err)
				}
			}
			before, sequence, storage := e.state.descriptors[a], e.state.descriptors[q], e.storage[a]
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION "+tc.actor)
			_, err := e.Execute(op, statement(t, "ALTER TABLE s1.t SET SCHEMA "+tc.destination), 0, nil)
			if tc.code != "" {
				renameWantError(t, err, tc.code, tc.message)
				if e.state.descriptors[a] != before || e.state.descriptors[q] != sequence || e.storage[a] != storage {
					t.Fatal("denial changed closure")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				dest := e.state.names[NameKey{ClassID: NamespaceClassID, Name: tc.destination}]
				if e.state.descriptors[a].Namespace() != dest || e.state.descriptors[q].Namespace() != dest || e.storage[a] != storage {
					t.Fatal("successful authorized move lost closure")
				}
			}
		})
	}
}
