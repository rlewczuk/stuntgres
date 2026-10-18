package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestAlterSchemaAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, actor, destination, code, message string
		source, target                          authorization.Privileges
	}{
		{"source_usage_only", "move_owner", "s2", "", "", authorization.UsagePrivilege, authorization.UsagePrivilege | authorization.CreatePrivilege},
		{"destination_create_only", "move_owner", "s2", "", "", authorization.UsagePrivilege, authorization.CreatePrivilege},
		{"destination_usage_only", "move_owner", "s2", "42501", "permission denied for schema s2", authorization.UsagePrivilege, authorization.UsagePrivilege},
		{"destination_neither", "move_owner", "s2", "42501", "permission denied for schema s2", authorization.UsagePrivilege, 0},
		{"source_no_usage", "move_owner", "s2", "42501", "permission denied for schema s1", authorization.CreatePrivilege, authorization.CreatePrivilege},
		{"same_schema_no_create", "move_owner", "s1", "42501", "permission denied for schema s1", authorization.UsagePrivilege, authorization.CreatePrivilege},
		{"schema_owner_no_source_usage", "move_schema_owner", "s2", "42501", "permission denied for schema s1", 0, authorization.CreatePrivilege},
		{"schema_owner_not_table_owner", "move_schema_owner", "s2", "42501", "must be owner of table t", authorization.UsagePrivilege, authorization.CreatePrivilege},
		{"nonowner_with_table_rights", "move_schema_owner", "s2", "42501", "must be owner of table t", authorization.UsagePrivilege, authorization.CreatePrivilege},
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
			set := func(name string, grantee authorization.PrincipalRef, privileges authorization.Privileges) {
				t.Helper()
				a := e.catalog.state.names[ids.NameKey{ClassID: ids.NamespaceClassID, Name: name}]
				acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: schemaOwner.Ref, Grantee: authorization.ACLGrantee{Role: grantee}, Privileges: privileges}}}
				if err := e.SetObjectACL(context.Background(), e.catalog.domain.Ref(e.catalog.state.epoch, a), acl); err != nil {
					t.Fatal(err)
				}
			}
			set("s1", owner.Ref, authorization.UsagePrivilege|authorization.CreatePrivilege)
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
			source := e.catalog.state.names[ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"}]
			a := NameAddressForTempMove(t, e, source, "t")
			q := NameAddressForTempMove(t, e, source, "q")
			if tc.name == "nonowner_with_table_rights" {
				table := e.catalog.state.descriptors[a].(*relationDescriptor)
				acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: table.owner, Grantee: authorization.ACLGrantee{Role: schemaOwner.Ref}, Privileges: authorization.SelectPrivilege}}}
				if err := e.SetObjectACL(context.Background(), e.catalog.domain.Ref(e.catalog.state.epoch, a), acl); err != nil {
					t.Fatal(err)
				}
			}
			before, sequence, storage := e.catalog.state.descriptors[a], e.catalog.state.descriptors[q], e.storage[a]
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION "+tc.actor)
			_, err := e.Execute(op, statement(t, "ALTER TABLE s1.t SET SCHEMA "+tc.destination), 0, nil)
			if tc.code != "" {
				renameWantError(t, err, tc.code, tc.message)
				if e.catalog.state.descriptors[a] != before || e.catalog.state.descriptors[q] != sequence || e.storage[a] != storage {
					t.Fatal("denial changed closure")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				dest := e.catalog.state.names[ids.NameKey{ClassID: ids.NamespaceClassID, Name: tc.destination}]
				if e.catalog.state.descriptors[a].Namespace() != dest || e.catalog.state.descriptors[q].Namespace() != dest || e.storage[a] != storage {
					t.Fatal("successful authorized move lost closure")
				}
			}
		})
	}
}
