package authorization

import (
	"reflect"
	"testing"
)

func TestPredefinedRoleInitialization(t *testing.T) {
	domain := &Domain{}
	root := Seed(domain)
	r := root.Read(domain.Principal(10))
	// Independent release expectations, deliberately not generated from the seed.
	want := map[string]uint64{
		"pg_checkpoint": 4544, "pg_create_subscription": 6304, "pg_database_owner": 6171,
		"pg_maintain": 6337, "pg_monitor": 3373, "pg_read_all_settings": 3374,
		"pg_read_all_stats": 3375, "pg_stat_scan_tables": 3377, "pg_read_all_data": 6181,
		"pg_write_all_data": 6182, "pg_read_server_files": 4569, "pg_write_server_files": 4570,
		"pg_execute_server_program": 4571, "pg_signal_autovacuum_worker": 6392,
		"pg_signal_backend": 4200, "pg_use_reserved_connections": 4550,
	}
	if len(r.state.roles) != 17 || len(r.state.names) != 17 {
		t.Fatal("initial authority", r.state)
	}
	for name, id := range want {
		d, ok := r.Lookup(name)
		expected := RoleDescriptor{Ref: domain.Principal(id), Name: name, AuthIDVersion: 1, Inherit: true, ConnectionLimit: -1, predefined: true}
		if !ok || d != expected {
			t.Fatalf("%s: %#v want %#v", name, d, expected)
		}
	}
	bootstrap, ok := r.Lookup("postgres")
	if !ok || bootstrap.Ref.id != 10 || bootstrap.AuthIDVersion != 1 || !bootstrap.Superuser || !bootstrap.Login {
		t.Fatal(bootstrap)
	}
	edges := []Membership{
		{Role: domain.Principal(3374), Member: domain.Principal(3373), Grantor: domain.Principal(10), Inherit: true, Set: true},
		{Role: domain.Principal(3375), Member: domain.Principal(3373), Grantor: domain.Principal(10), Inherit: true, Set: true},
		{Role: domain.Principal(3377), Member: domain.Principal(3373), Grantor: domain.Principal(10), Inherit: true, Set: true},
	}
	if !reflect.DeepEqual(r.Memberships(), edges) {
		t.Fatal("seed provenance", r.Memberships())
	}
	external := r.Memberships()
	external[0].Admin = true
	if r.Memberships()[0].Admin {
		t.Fatal("caller mutated seed graph")
	}
}
