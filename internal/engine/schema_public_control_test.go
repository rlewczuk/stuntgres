package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestSchemaPublicLifecycleControls(t *testing.T) {
	sameACL := func(a, b authorization.ACLValue) bool {
		return a.Explicit == b.Explicit && slices.Equal(a.Entries, b.Entries)
	}
	for _, tc := range []struct {
		name      string
		setup     []string
		namespace string
		table     bool
		owner     string
		explicit  bool
	}{
		{"renamed", []string{"CREATE TABLE public.t(id INT)", "INSERT INTO public.t VALUES(7)", "ALTER SCHEMA public RENAME TO renamed_public"}, "renamed_public", true, "pg_database_owner", true},
		{"renamed_empty", []string{"ALTER SCHEMA public RENAME TO renamed_public"}, "renamed_public", false, "pg_database_owner", true},
		{"missing", []string{"DROP SCHEMA public"}, "", false, "", false},
		{"recreated", []string{"DROP SCHEMA public", "CREATE SCHEMA public", "CREATE TABLE public.t(id INT)", "INSERT INTO public.t VALUES(7)"}, "public", true, "postgres", false},
		{"recreated_empty", []string{"DROP SCHEMA public", "CREATE SCHEMA public AUTHORIZATION ordinary"}, "public", false, "ordinary", false},
		{"recreated_ordinary", []string{"DROP SCHEMA public", "CREATE SCHEMA public AUTHORIZATION ordinary", "SET SESSION AUTHORIZATION ordinary", "CREATE TABLE public.t(id INT)", "INSERT INTO public.t VALUES(7)", "RESET SESSION AUTHORIZATION"}, "public", true, "ordinary", false},
		{"transferred", []string{"ALTER SCHEMA public OWNER TO ordinary"}, "public", false, "ordinary", true},
		{"renamed_transferred", []string{"CREATE TABLE public.t(id INT)", "INSERT INTO public.t VALUES(7)", "ALTER SCHEMA public RENAME TO renamed_public", "ALTER SCHEMA renamed_public OWNER TO ordinary"}, "renamed_public", true, "ordinary", true},
		{"renamed_and_recreated", []string{"ALTER SCHEMA public RENAME TO renamed_public", "CREATE SCHEMA public AUTHORIZATION ordinary", "SET SESSION AUTHORIZATION ordinary", "CREATE TABLE public.t(id INT)", "INSERT INTO public.t VALUES(7)", "RESET SESSION AUTHORIZATION"}, "public", true, "ordinary", false},
		{"absent_nonpublic", []string{"DROP SCHEMA public", "CREATE SCHEMA nonpublic CREATE TABLE t(id INT)", "INSERT INTO nonpublic.t VALUES(9)"}, "nonpublic", true, "postgres", false},
		{"catalog_relocated", []string{"ALTER SCHEMA pg_catalog RENAME TO moved_catalog", "ALTER SCHEMA public RENAME TO renamed_public", "CREATE SCHEMA public AUTHORIZATION ordinary", "CREATE SCHEMA stuntgres_restore_namespace_0_0"}, "renamed_public", false, "pg_database_owner", true},
		{"transferred_collision", []string{"ALTER SCHEMA public RENAME TO renamed_public", "ALTER SCHEMA renamed_public OWNER TO ordinary", "CREATE SCHEMA public AUTHORIZATION ordinary_other", "SET SESSION AUTHORIZATION ordinary_other", "CREATE TABLE public.t(id INT)", "INSERT INTO public.t VALUES(7)", "RESET SESSION AUTHORIZATION"}, "public", true, "ordinary_other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE USER ordinary")
			run(t, e, "CREATE USER ordinary_other")
			for _, q := range tc.setup {
				run(t, e, q)
			}
			var acl authorization.ACLValue
			if tc.namespace != "" {
				acl = e.CatalogRead().state.descriptors[schemaRef(t, e, tc.namespace).Address].(*namespaceDescriptor).acl
			}
			before := e.CatalogRead()
			names := make(map[string]authorization.ACLValue)
			owners := make(map[string]authorization.PrincipalRef)
			for _, descriptor := range before.state.descriptors {
				if ns, ok := descriptor.(*namespaceDescriptor); ok {
					names[ns.name], owners[ns.name] = ns.acl, ns.owner
				}
			}
			var dump bytes.Buffer
			if err := e.Dump(context.Background(), &dump); err != nil {
				t.Fatal(err)
			}
			if err := e.Reset(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(dump.String(), err)
			}
			after := e.CatalogRead()
			if after.Epoch() == before.Epoch() {
				t.Fatal("Restore reused catalog epoch")
			}
			gotNames := make(map[string]authorization.ACLValue)
			for _, descriptor := range after.state.descriptors {
				if ns, ok := descriptor.(*namespaceDescriptor); ok {
					gotNames[ns.name] = ns.acl
					if ns.owner != owners[ns.name] {
						t.Fatal("namespace owner changed", ns.name)
					}
				}
			}
			if !maps.EqualFunc(gotNames, names, sameACL) {
				t.Fatalf("namespace/ACL map %+v want %+v", gotNames, names)
			}
			if tc.namespace == "" {
				if _, exists := e.CatalogRead().state.names[NameKey{ClassID: NamespaceClassID, Name: "public"}]; exists {
					t.Fatal("Restore invented public")
				}
			} else {
				d := e.CatalogRead().state.descriptors[schemaRef(t, e, tc.namespace).Address].(*namespaceDescriptor)
				owner, _ := e.Roles().Lookup(tc.owner)
				if d.owner != owner.Ref || d.acl.Explicit != tc.explicit || !sameACL(d.acl, acl) {
					t.Fatalf("owner/ACL changed: %+v want owner %v ACL %+v", d, owner.Ref, acl)
				}
				if tc.table {
					wantValue := int64(7)
					if tc.name == "absent_nonpublic" {
						wantValue = 9
					}
					if got := tuples(run(t, e, "SELECT id FROM "+tc.namespace+".t")); !reflect.DeepEqual(got, [][]driver.Value{{wantValue}}) {
						t.Fatal(got)
					}
					td := e.CatalogRead().state.descriptors[roleTableRef(t, e, tc.namespace+".t").Address].(*relationDescriptor)
					// Namespace OWNER does not restamp the independently owned table.
					oldTable := before.state.descriptors[before.state.names[NameKey{ClassID: RelationClassID, Namespace: before.state.names[NameKey{ClassID: NamespaceClassID, Name: tc.namespace}], Name: "t"}]].(*relationDescriptor)
					if td.owner != oldTable.owner || td.acl.Explicit || len(td.acl.Entries) != 0 {
						t.Fatal("table owner/ACL changed")
					}
				}
			}
			var again bytes.Buffer
			if err := e.Dump(context.Background(), &again); err != nil || again.String() != dump.String() {
				t.Fatal("unstable roundtrip", err, dump.String(), again.String())
			}
		})
	}
	t.Run("restore_continuation", func(t *testing.T) {
		for _, destination := range []string{"fresh", "lower_counter"} {
			t.Run(destination, func(t *testing.T) {
				source := New()
				for _, q := range []string{
					"CREATE USER ordinary",
					"ALTER SCHEMA public RENAME TO retained_public",
					"ALTER SCHEMA retained_public OWNER TO ordinary",
					"CREATE SCHEMA restored_a AUTHORIZATION ordinary",
					"CREATE SCHEMA restored_b AUTHORIZATION ordinary",
					"CREATE SCHEMA restored_data AUTHORIZATION ordinary CREATE TABLE t(id INT NOT NULL)",
					"INSERT INTO restored_data.t VALUES(7),(9)",
				} {
					run(t, source, q)
				}
				var dump bytes.Buffer
				if err := source.Dump(context.Background(), &dump); err != nil {
					t.Fatal(err)
				}
				e := New()
				run(t, e, "CREATE USER ordinary")
				if destination == "lower_counter" {
					run(t, e, "CREATE SCHEMA spent")
					run(t, e, "DROP SCHEMA spent")
				}
				counter := e.nextOID
				if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				if e.nextOID != counter {
					t.Fatal("Restore changed destination nonreuse counter")
				}
				before := e.CatalogRead()
				owner, _ := e.Roles().Lookup("ordinary")
				for _, name := range []string{"restored_a", "restored_b", "restored_data", "retained_public"} {
					ns := before.state.descriptors[schemaRef(t, e, name).Address].(*namespaceDescriptor)
					wantACL := authorization.ACLValue{}
					if name == "retained_public" {
						wantACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
							{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: owner.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege},
							{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
						}}
					}
					if ns.owner != owner.Ref || !sameACL(ns.acl, wantACL) {
						t.Fatal("restored owner/ACL", name, ns)
					}
				}
				table := before.state.descriptors[roleTableRef(t, e, "restored_data.t").Address].(*relationDescriptor)
				if table.owner != owner.Ref || table.acl.Explicit || len(table.acl.Entries) != 0 {
					t.Fatal("restored table owner/ACL", table)
				}
				for _, q := range []string{"CREATE SCHEMA additional", "CREATE SCHEMA nested CREATE TABLE t(id INT NOT NULL) CREATE TABLE u(id INT)"} {
					if got := run(t, e, q).Count; got != 0 {
						t.Fatal(q, got)
					}
				}
				if got := run(t, e, "INSERT INTO nested.t VALUES(13),(17)").Count; got != 2 {
					t.Fatal("insert count", got)
				}
				if got := run(t, e, "INSERT INTO nested.u VALUES(19)").Count; got != 1 {
					t.Fatal("insert count", got)
				}
				after := e.CatalogRead()
				for address, descriptor := range before.state.descriptors {
					if after.state.descriptors[address] != descriptor {
						t.Fatal("continuation changed restored descriptor", address)
					}
				}
				postgres, _ := e.Roles().Lookup("postgres")
				for _, name := range []string{"additional", "nested"} {
					namespace := after.state.descriptors[schemaRef(t, e, name).Address].(*namespaceDescriptor)
					if namespace.owner != postgres.Ref || namespace.acl.Explicit || len(namespace.acl.Entries) != 0 {
						t.Fatal("new namespace owner/ACL", name, namespace)
					}
				}
				byObjectID := make(map[OID]ObjectAddress)
				for address := range after.state.descriptors {
					if address.ClassID != NamespaceClassID && address.ClassID != RelationClassID {
						continue
					}
					if prior, exists := byObjectID[address.ObjectID]; exists {
						t.Fatal("shared identity collision", prior, address)
					}
					byObjectID[address.ObjectID] = address
				}
				for _, tc := range []struct {
					table string
					rows  [][]driver.Value
				}{
					{"restored_data.t", [][]driver.Value{{int64(7)}, {int64(9)}}},
					{"nested.t", [][]driver.Value{{int64(13)}, {int64(17)}}},
					{"nested.u", [][]driver.Value{{int64(19)}}},
				} {
					result := run(t, e, "SELECT id FROM "+tc.table+" ORDER BY id")
					if result.Count != int64(len(tc.rows)) || !reflect.DeepEqual(tuples(result), tc.rows) {
						t.Fatal(tc.table, result.Count, tuples(result))
					}
				}
				additional := schemaRef(t, e, "additional")
				run(t, e, "DROP SCHEMA additional")
				run(t, e, "CREATE SCHEMA additional")
				if schemaRef(t, e, "additional").Address.ObjectID <= additional.Address.ObjectID {
					t.Fatal("recreated namespace reused identity")
				}
				run(t, e, "DROP SCHEMA additional,nested CASCADE")
				var again bytes.Buffer
				if err := e.Dump(context.Background(), &again); err != nil || again.String() != dump.String() {
					t.Fatal("restored snapshot changed", err, again.String(), dump.String())
				}
			})
		}
	})
}
