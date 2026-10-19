package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func predefinedUtilityError(t *testing.T, e *Engine, op *Operation, stmt parser.Statement, code, message, detail string) {
	t.Helper()
	before, next := e.roles.published, e.roles.nextPrincipal
	result, err := e.Execute(op, stmt, 0, nil)
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || result.Count != 0 || result.Rows != nil {
		t.Fatalf("diagnostic: %#v / %v, want %#v; result %#v", diagnostic, err, want, result)
	}
	if e.roles.published != before || e.roles.nextPrincipal != next {
		t.Fatal("failed utility published authority or allocator")
	}
}

func predefinedMembership(target, member string) *parser.RoleMembership {
	return &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: target}}, Members: []parser.RoleSpec{{Name: member}}}
}

func TestPredefinedRoleInitialization(t *testing.T) {
	e := New()
	rPublication := e.roles.published
	r := e.Roles()
	// Independent release expectations, deliberately not generated from the seed.
	want := map[string]uint64{
		"pg_checkpoint": 4544, "pg_create_subscription": 6304, "pg_database_owner": 6171,
		"pg_maintain": 6337, "pg_monitor": 3373, "pg_read_all_settings": 3374,
		"pg_read_all_stats": 3375, "pg_stat_scan_tables": 3377, "pg_read_all_data": 6181,
		"pg_write_all_data": 6182, "pg_read_server_files": 4569, "pg_write_server_files": 4570,
		"pg_execute_server_program": 4571, "pg_signal_autovacuum_worker": 6392,
		"pg_signal_backend": 4200, "pg_use_reserved_connections": 4550,
	}
	if r.RoleCount() != 17 || r.RoleCount() != 17 || rPublication.revision != 1 || e.roles.nextPrincipal != 6393 {
		t.Fatal("initial authority", rPublication, e.roles.nextPrincipal)
	}
	for name, id := range want {
		d, ok := r.Lookup(name)
		expected := authorization.RoleDescriptor{Ref: e.roles.domain.Principal(id), Name: name, Inherit: true, ConnectionLimit: -1}
		if !ok || d.Ref != expected.Ref || !d.Predefined() || func() bool {
			expectedDescriptor := d
			expectedDescriptor.Ref = expected.Ref
			return expectedDescriptor.Name != expected.Name || expectedDescriptor.Login != expected.Login || expectedDescriptor.Superuser != expected.Superuser || expectedDescriptor.CreateDB != expected.CreateDB || expectedDescriptor.CreateRole != expected.CreateRole || expectedDescriptor.Inherit != expected.Inherit || expectedDescriptor.Replication != expected.Replication || expectedDescriptor.BypassRLS != expected.BypassRLS || expectedDescriptor.ConnectionLimit != expected.ConnectionLimit || expectedDescriptor.Password != expected.Password || expectedDescriptor.ValidUntil != expected.ValidUntil || expectedDescriptor.HasValidUntil != expected.HasValidUntil || expectedDescriptor.Comment != expected.Comment
		}() {
			t.Fatalf("%s: %#v want %#v", name, d, expected)
		}
	}
	bootstrap, ok := r.Lookup("postgres")
	if !ok || bootstrap.Ref.ID() != 10 || !bootstrap.Superuser || !bootstrap.Login {
		t.Fatal(bootstrap)
	}
	edges := []authorization.Membership{
		{Role: e.roles.domain.Principal(3374), Member: e.roles.domain.Principal(3373), Grantor: e.roles.domain.Principal(10), Inherit: true, Set: true},
		{Role: e.roles.domain.Principal(3375), Member: e.roles.domain.Principal(3373), Grantor: e.roles.domain.Principal(10), Inherit: true, Set: true},
		{Role: e.roles.domain.Principal(3377), Member: e.roles.domain.Principal(3373), Grantor: e.roles.domain.Principal(10), Inherit: true, Set: true},
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

func TestPredefinedRoleAllocation(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for i := range 1030 {
		d := roleAttributeCreate(t, e, op, fmt.Sprintf("allocated_%d", i), false, parser.RoleOptions{})
		if d.Ref.ID() != uint64(6393+i) || d.Predefined() {
			t.Fatal("ordinary identity collided", d)
		}
	}
	if e.roles.nextPrincipal != 7423 || e.roles.published.root.Read(authorization.PrincipalRef{}).RoleCount() != 1047 {
		t.Fatal(e.roles.nextPrincipal, e.roles.published.root.Read(authorization.PrincipalRef{}).RoleCount())
	}
	for _, name := range []string{"pg_monitor", "pg_maintain", "pg_signal_autovacuum_worker"} {
		d, ok := e.Roles().Lookup(name)
		if !ok || !d.Predefined() {
			t.Fatal("allocation replaced builtin", name, d)
		}
	}
	for _, next := range []uint64{0, ^uint64(0), 6393} {
		e.roles.nextPrincipal = next
		roleAttributeFailure(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: "exhausted"}}, "")
	}
	e.roles.nextPrincipal = 7423
	exhausted := *e.roles.published
	exhausted.revision = ^uint64(0)
	e.roles.published = &exhausted
	roleAttributeFailure(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: "revision_exhausted"}}, "")
	floor := New()
	floor.roles.nextPrincipal = 3373
	candidate, err := floor.roles.principalCandidateLocked()
	if err != nil || candidate != (floor.roles.domain.Principal(6393)) || floor.roles.nextPrincipal != 3373 {
		t.Fatal("private checked candidate", candidate, err)
	}
	canceled := New()
	actor, closeActor := roleOperation(t, canceled, "postgres")
	defer closeActor()
	before, allocator := canceled.roles.published, canceled.roles.nextPrincipal
	ctx, cancel := context.WithCancel(actor.Context())
	defer cancel()
	fence := &identityRolePublicationContext{Context: ctx, session: actor.session, cancel: cancel}
	actor.ctx = fence
	_, err = canceled.executeRoleAttributes(&StatementScope{op: actor}, &parser.CreateRole{Name: parser.Identifier{Text: "canceled"}}, 0)
	if !fence.observed || !errors.Is(err, context.Canceled) || canceled.roles.published != before || canceled.roles.nextPrincipal != allocator {
		t.Fatal("publication cancellation", err, fence.observed)
	}
}

func TestPredefinedImplicitDatabaseOwner(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	reader := roleAttributeCreate(t, e, op, "owner_path", false, parser.RoleOptions{})
	n := cloneRoleFixture(e.roles.published)
	bootstrap, owner := e.roles.domain.Principal(10), e.roles.domain.Principal(6171)
	d := fixtureRole(n, bootstrap)
	d.Superuser = false // Exercise the implicit rule, not the independent SU bypass.
	putFixtureRole(n, d)
	setFixtureEdges(n, nil)
	r := e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), n)
	for _, inherit := range []bool{false, true} {
		got, err := r.RoleClosure(context.Background(), bootstrap, inherit)
		if err != nil || !reflect.DeepEqual(got, []authorization.PrincipalRef{bootstrap, owner}) {
			t.Fatal("empty graph closure", got, err)
		}
	}
	if ok, err := r.InheritsRole(bootstrap, owner); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := r.CanSetRole(bootstrap, owner); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := r.AdminOfRole(bootstrap, owner); ok || err != nil {
		t.Fatal("implicit ADMIN", ok, err)
	}
	if ok, err := r.AdminOfRole(owner, owner); ok || err != nil {
		t.Fatal("self ADMIN", ok, err)
	}
	for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}} {
		setFixtureEdges(n, []authorization.Membership{{Role: bootstrap, Member: reader.Ref, Grantor: bootstrap, Inherit: flags[0], Set: flags[1]}})
		got, err := r.RoleClosure(context.Background(), reader.Ref, false)
		if err != nil || !reflect.DeepEqual(got, []authorization.PrincipalRef{reader.Ref, bootstrap, owner}) {
			t.Fatal("visited-owner MEMBER closure", got, err)
		}
		if ok, err := r.InheritsRole(reader.Ref, owner); ok != flags[0] || err != nil {
			t.Fatal("INHERIT flags", ok, err)
		}
		if ok, err := r.CanSetRole(reader.Ref, owner); ok != flags[1] || err != nil {
			t.Fatal("SET flags", ok, err)
		}
		if ok, err := r.AdminOfRole(reader.Ref, owner); ok || err != nil {
			t.Fatal("path became ADMIN", ok, err)
		}
	}
	foreign := New().roles.domain.Principal(6171)
	for _, bad := range []authorization.PrincipalRef{{}, foreign, e.roles.domain.Principal(999999)} {
		if _, err := e.Roles().InheritsRole(bootstrap, bad); err == nil {
			t.Fatal("SU accepted invalid target", bad)
		}
		if _, err := e.Roles().CanSetRole(bootstrap, bad); err == nil {
			t.Fatal("SET accepted invalid target", bad)
		}
		if _, err := e.Roles().AdminOfRole(bootstrap, bad); err == nil {
			t.Fatal("ADMIN accepted invalid target", bad)
		}
	}
	setFixtureEdges(n, nil)
	retireFixtureRole(n, owner)
	if _, err := r.RoleClosure(context.Background(), bootstrap, false); err == nil {
		t.Fatal("implicit edge accepted stale descriptor")
	}
}

func TestPredefinedMembershipRestrictions(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleAttributeCreate(t, e, op, "member", false, parser.RoleOptions{})
	predefinedUtilityError(t, e, op, predefinedMembership("pg_database_owner", "member"), "0A000", `role "pg_database_owner" cannot have explicit members`, "")
	predefinedUtilityError(t, e, op, predefinedMembership("member", "pg_database_owner"), "0A000", `role "pg_database_owner" cannot be a member of any role`, "")
	mixed := predefinedMembership("pg_read_all_data", "member")
	mixed.Targets = append(mixed.Targets, parser.RoleSpec{Name: "pg_database_owner"})
	predefinedUtilityError(t, e, op, mixed, "0A000", `role "pg_database_owner" cannot have explicit members`, "")
	alias := predefinedMembership("pg_database_owner", "member")
	alias.Alias = true
	predefinedUtilityError(t, e, op, alias, "42939", `role name "pg_database_owner" is reserved`, "Cannot alter reserved roles.")
	initial := &parser.CreateRole{Name: parser.Identifier{Text: "failed_initial"}, Options: parser.RoleOptions{InitialMemberships: []parser.InitialRoleMembership{{Kind: "in", Members: []parser.RoleSpec{{Name: "pg_database_owner"}}}}}}
	predefinedUtilityError(t, e, op, initial, "0A000", `role "pg_database_owner" cannot have explicit members`, "")
	if _, exists := e.Roles().Lookup("failed_initial"); exists {
		t.Fatal("initial member leaked")
	}
	for _, name := range []string{
		"pg_checkpoint", "pg_create_subscription", "pg_database_owner", "pg_maintain",
		"pg_monitor", "pg_read_all_settings", "pg_read_all_stats", "pg_stat_scan_tables",
		"pg_read_all_data", "pg_write_all_data", "pg_read_server_files", "pg_write_server_files",
		"pg_execute_server_program", "pg_signal_autovacuum_worker", "pg_signal_backend", "pg_use_reserved_connections",
	} {
		predefinedUtilityError(t, e, op, &parser.DropRole{Names: []string{"member", name}}, "2BP01", "cannot drop role "+name+" because it is required by the database system", "")
	}
	predefinedUtilityError(t, e, op, &parser.DropRole{Names: []string{"pg_maintain", "absent"}}, "42704", `role "absent" does not exist`, "")
}

func TestPredefinedRoleSnapshots(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	retainedPublication := e.roles.published
	retained := e.Roles()
	revoke := predefinedMembership("pg_read_all_settings", "pg_monitor")
	revoke.Revoke = true
	roleAttributeApply(t, e, op, revoke)
	if len(retained.Memberships()) != 3 || len(e.Roles().Memberships()) != 2 || e.roles.published.revision != retainedPublication.revision+1 {
		t.Fatal("snapshot publication")
	}
	before, err := retained.RoleClosure(context.Background(), retained.LookupRef("pg_monitor"), true)
	if err != nil || len(before) != 4 {
		t.Fatal(before, err)
	}
	after, err := e.Roles().RoleClosure(context.Background(), e.roles.published.root.Read(authorization.PrincipalRef{}).LookupRef("pg_monitor"), true)
	if err != nil || len(after) != 3 {
		t.Fatal(after, err)
	}
	reader := roleAttributeCreate(t, e, op, "retired", false, parser.RoleOptions{})
	old := e.Roles()
	roleAttributeApply(t, e, op, &parser.DropRole{Names: []string{"retired"}})
	if _, err := old.Descriptor(reader.Ref); err != nil {
		t.Fatal("retained principal", err)
	}
	if _, err := e.Roles().Descriptor(reader.Ref); err == nil {
		t.Fatal("retired principal remained valid")
	}
	if _, err := New().Roles().Descriptor(e.roles.published.root.Read(authorization.PrincipalRef{}).LookupRef("pg_read_all_data")); err == nil {
		t.Fatal("fixed OID bypassed engine identity")
	}
}

func TestPredefinedNoBypassRLS(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	reader := roleAttributeCreate(t, e, op, "rls_reader", false, parser.RoleOptions{})
	for _, role := range []string{"pg_read_all_data", "pg_write_all_data", "pg_maintain", "pg_monitor"} {
		roleAttributeApply(t, e, op, predefinedMembership(role, reader.Name))
		d, _ := e.Roles().Lookup(role)
		if d.BypassRLS || d.Superuser {
			t.Fatal("predefined attribute elevation", d)
		}
	}
	d, _ := e.Roles().Descriptor(reader.Ref)
	if d.BypassRLS || d.Superuser {
		t.Fatal("membership changed member attributes", d)
	}
}
