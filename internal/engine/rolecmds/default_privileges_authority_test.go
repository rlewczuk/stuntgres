package rolecmds_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestDefaultPrivilegesArrayAuthority(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.actor(f.owner)
	create := func(name string) (engine.AuthorizationView, *catalog.Type, *catalog.Type) {
		t.Helper()
		stage := f.e.BeginCatalogStage(f.e.CatalogRead())
		f.e.Mu.Lock()
		auth, err := f.e.AuthorizationLocked(f.op)
		if err == nil {
			err = typedef.StageCreateEnumLocked(engine.DDLHost{E: f.e, Op: f.op, CatalogStage: stage}, auth, &parser.CreateEnum{
				Name:   parser.ObjectName{Parts: []parser.Identifier{{Text: "default_schema"}, {Text: name}}},
				Labels: []parser.EnumLabel{{Text: "a"}},
			})
		}
		if err == nil {
			auth.Catalog = f.e.StageRead(stage)
			auth.Recapture(auth.Catalog)
		}
		f.e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		base := auth.Catalog.MustType(auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: f.schema, Name: name}))
		array := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
		if !authorization.MatchesDefaultBaseline(array.ACL, f.owner, authorization.DefaultTypes, false) || access.DescriptorRoleDependencies(array, f.reader, nil) {
			t.Fatal("associated enum array consumed selected TYPES ACL", array.ACL)
		}
		auth.State.CurrentUser = f.reader
		return auth, base, array
	}
	check := func(auth engine.AuthorizationView, base, array *catalog.Type, allowed bool) {
		t.Helper()
		for _, descriptor := range []*catalog.Type{base, array} {
			ref := f.e.Catalog.Domain.Ref(auth.Catalog.Epoch(), descriptor.Address())
			err := auth.CanUse(ref)
			if allowed {
				if err != nil {
					t.Fatal("element default ACL should permit scalar and associated array", descriptor.Name(), err)
				}
			} else {
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for type default_schema." + base.Name()}
				var actual *sqlstate.Error
				if !errors.As(err, &actual) || !reflect.DeepEqual(actual, want) {
					t.Fatal("array must redirect to denied element, not its own PUBLIC baseline", descriptor.Name(), err, want)
				}
			}
		}
	}
	oldAuth, oldBase, oldArray := create("array_old")
	check(oldAuth, oldBase, oldArray, true)
	revoke := defaultUnitStatement("types", "usage", []string{"default_owner"}, nil, []string{"PUBLIC"})
	revoke.Revoke = true
	if err := f.apply(revoke); err != nil {
		t.Fatal(err)
	}
	blockedAuth, blockedBase, blockedArray := create("array_blocked")
	check(blockedAuth, blockedBase, blockedArray, false)
	scoped := f.grant(t, "types", "usage", []string{"default_schema"})
	scopedAuth, scopedBase, scopedArray := create("array_scoped")
	check(scopedAuth, scopedBase, scopedArray, true)
	if !access.DescriptorRoleDependencies(scopedBase, f.reader, nil) {
		t.Fatal("element lost scoped named ACL dependency")
	}
	scoped.Revoke = true
	if err := f.apply(scoped); err != nil {
		t.Fatal(err)
	}
	nextAuth, nextBase, nextArray := create("array_next")
	check(nextAuth, nextBase, nextArray, false)
	check(oldAuth, oldBase, oldArray, true)
	check(scopedAuth, scopedBase, scopedArray, true)
}

func TestDefaultPrivilegesTemporaryNamespace(t *testing.T) {
	f := newDefaultUnitFixture(t)
	roleExecute(t, f.e, f.op, "CREATE TEMP TABLE default_authority_seed(v INT)")
	f.schema = f.op.Session.TemporaryNamespace
	state := catalog.CloneState(f.e.Catalog.State)
	namespace := *state.MustNamespace(f.schema)
	assertTemporaryNamespacePair(t, f.e, f.op.Session.SessionToken, f.schema)
	namespace.ACL = authorization.ACLValue{Explicit: true}
	state.SetDescriptor(f.schema, &namespace)
	f.e.Mu.Lock()
	auth, err := f.e.AuthorizationLocked(f.op)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	auth.Catalog = f.e.Catalog.Read(state)
	auth.Recapture(auth.Catalog)
	auth.State.CurrentUser = f.reader
	ref := f.e.Catalog.Domain.Ref(state.Epoch(), f.schema)
	for _, privilege := range []authorization.Privileges{authorization.UsagePrivilege, authorization.CreatePrivilege, authorization.UsagePrivilege | authorization.CreatePrivilege} {
		if err := auth.Check(access.Requirement{Object: ref, Privileges: privilege}); err != nil {
			t.Fatal("physical namespace authority depended on current actor", privilege, err)
		}
	}
	if rights, err := auth.Effective(ref, true); err != nil || rights != 0 {
		t.Fatal("physical authority invented namespace delegation", rights, err)
	}
	defaultUnitDiagnostic(t, auth.Check(access.Requirement{Object: ref, OwnerOnly: true}), "42501", "must be owner of schema "+namespace.Name())
	for _, token := range []ids.SessionToken{
		{},
		f.e.Catalog.Domain.Session(f.op.Session.SessionToken.ID() + 1),
		engine.New().Catalog.Domain.Session(f.op.Session.SessionToken.ID()),
		f.e.Catalog.Domain.PrivateSession(f.op.Session),
	} {
		peer := auth
		peer.Session = &session.Session{SessionToken: token}
		defaultUnitDiagnostic(t, peer.CanUse(ref), "42501", "permission denied for schema "+namespace.Name())
	}
	// Even an exact namespace token cannot elevate zero/foreign/private
	// maintenance identities into a physical backend.
	for _, token := range []ids.SessionToken{{}, engine.New().Catalog.Domain.Session(1), f.e.Catalog.Domain.PrivateSession(f.op.Session)} {
		candidate := catalog.CloneState(state)
		invalidNamespace := namespace
		invalidNamespace.Temporary = token
		candidate.SetDescriptor(f.schema, &invalidNamespace)
		invalid := auth
		invalid.Catalog = f.e.Catalog.Read(candidate)
		invalid.Recapture(invalid.Catalog)
		invalid.Session = &session.Session{SessionToken: token}
		defaultUnitDiagnostic(t, invalid.CanUse(ref), "42501", "permission denied for schema "+namespace.Name())
	}
	withoutSession := auth
	withoutSession.Session = nil
	defaultUnitDiagnostic(t, withoutSession.CanUse(ref), "42501", "permission denied for schema "+namespace.Name())
	ordinary := catalog.CloneState(state)
	permanent := namespace
	permanent.Temporary = ids.SessionToken{}
	ordinary.SetDescriptor(f.schema, &permanent)
	ordinaryAuth := auth
	ordinaryAuth.Catalog = f.e.Catalog.Read(ordinary)
	ordinaryAuth.Recapture(ordinaryAuth.Catalog)
	defaultUnitDiagnostic(t, ordinaryAuth.CanUse(ref), "42501", "permission denied for schema "+namespace.Name())
	foreign := ref
	foreign = engine.New().Catalog.Domain.Ref(foreign.Epoch, foreign.Address)
	if err := auth.CanUse(foreign); err == nil {
		t.Fatal("physical namespace authority admitted foreign object")
	}
	staleActor := auth
	staleActor.State.CurrentUser = f.e.RoleStore.Domain.Principal(999999)
	if err := staleActor.CanUse(ref); err == nil {
		t.Fatal("physical namespace authority admitted stale actor")
	}
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20111}, ObjectNamespace: f.schema, ObjectName: "temp_acl_denied", Owner: f.owner, ObjectRevision: 1, RelationKind: catalog.TableRelation, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}}
	state.SetDescriptor(table.Address(), table)
	tableRef := f.e.Catalog.Domain.Ref(state.Epoch(), table.Address())
	defaultUnitDiagnostic(t, auth.Check(access.Requirement{Object: tableRef, Privileges: authorization.SelectPrivilege}), "42501", "permission denied for table temp_acl_denied")
}

func newDefaultUnitFixture(t *testing.T) defaultUnitFixture {
	t.Helper()
	e := engine.New()
	roles := cloneRoleFixture(e.RoleStore.Published)
	refs := make([]authorization.PrincipalRef, 3)
	for i, name := range []string{"default_owner", "default_reader", "default_member"} {
		ref := e.RoleStore.Domain.Principal(uint64(20001 + i))
		refs[i] = ref
		putFixtureRole(roles, authorization.RoleDescriptor{Ref: ref, Name: name, Login: true, Inherit: true, ConnectionLimit: -1})

	}
	setFixtureEdges(roles, append(append([]authorization.Membership(nil), fixtureEdges(roles)...), authorization.Membership{Role: refs[0], Member: refs[2], Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Inherit: true, Set: true}))
	e.RoleStore.PublishLocked(roles)
	schema := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20010}
	next := catalog.CloneState(e.Catalog.State)
	namespace := &catalog.Namespace{ObjectAddress: schema, ObjectName: "default_schema", ObjectRevision: 2, Owner: refs[0]}
	next.SetDescriptor(schema, namespace)
	next.BindName(catalog.DescriptorNameKey(namespace), schema)
	e.Catalog.PublishLocked(next)
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	return defaultUnitFixture{e: e, op: op, owner: refs[0], reader: refs[1], member: refs[2], schema: schema}
}

func defaultUnitStatement(class, privilege string, creators, schemas, grantees []string) *parser.AlterDefaultPrivileges {
	s := &parser.AlterDefaultPrivileges{Class: class, Privileges: []parser.GrantPrivilege{{Name: privilege, All: privilege == "all"}}}
	if creators != nil {
		option := parser.DefaultPrivilegesOption{Roles: make([]parser.RoleSpec, len(creators))}
		for i, name := range creators {
			option.Roles[i] = parser.RoleSpec{Name: name}
		}
		s.Options = append(s.Options, option)
	}
	if schemas != nil {
		option := parser.DefaultPrivilegesOption{Schemas: make([]parser.Identifier, len(schemas))}
		for i, name := range schemas {
			option.Schemas[i] = parser.Identifier{Text: name}
		}
		s.Options = append(s.Options, option)
	}
	for _, name := range grantees {
		s.Grantees = append(s.Grantees, parser.GrantGrantee{Public: name == "PUBLIC", Role: parser.RoleSpec{Name: name}})
	}
	return s
}

func assertTemporaryNamespacePair(t *testing.T, e *engine.Engine, token ids.SessionToken, table ids.ObjectAddress) ids.ObjectAddress {
	t.Helper()
	ns, ok := e.Catalog.State.Namespace(table)
	if !ok || ns.Temporary != token || ns.TemporaryToast || ns.Owner.ID() != 10 || ns.ACL.Explicit || len(ns.ACL.Entries) != 0 {
		t.Fatal("real table TEMP authority", ns)
	}
	suffix, ok := strings.CutPrefix(ns.Name(), "pg_temp_")
	if !ok {
		t.Fatal(ns.Name())
	}
	var toast ids.ObjectAddress
	count := 0
	for a, d := range e.Catalog.State.Descriptors() {
		other, ok := d.(*catalog.Namespace)
		if !ok || other.Temporary != token {
			continue
		}
		count++
		if other.TemporaryToast {
			if other.Name() != "pg_toast_temp_"+suffix || other.Owner != ns.Owner || other.ACL.Explicit || len(other.ACL.Entries) != 0 || other.Address() == table {
				t.Fatal("real toast TEMP authority", other)
			}
			toast = a
		}
		if e.Catalog.State.NameAddress(catalog.DescriptorNameKey(other)) != a {
			t.Fatal("TEMP name identity", other)
		}
	}
	if count != 2 || toast == (ids.ObjectAddress{}) {
		t.Fatal("TEMP pair cardinality", count, toast)
	}
	return toast
}

func defaultUnitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" {
		t.Fatalf("diagnostic = %#v; want %s %q", err, code, message)
	}
}

type defaultUnitFixture struct {
	e                     *engine.Engine
	op                    *session.Operation
	owner, reader, member authorization.PrincipalRef
	schema                ids.ObjectAddress
}

func (f defaultUnitFixture) apply(s *parser.AlterDefaultPrivileges) error {
	_, err := f.e.Execute(f.op, s, 0, nil)
	return err
}

func (f defaultUnitFixture) grant(t *testing.T, class, privilege string, schemas []string) *parser.AlterDefaultPrivileges {
	t.Helper()
	s := defaultUnitStatement(class, privilege, []string{"default_owner"}, schemas, []string{"default_reader"})
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f defaultUnitFixture) actor(ref authorization.PrincipalRef) {
	f.e.Mu.Lock()
	f.op.Session.Mu.Lock()
	f.op.Session.Authorization.State.CurrentUser = ref
	f.op.Session.Authorization.Revision++
	f.op.Session.Mu.Unlock()
	f.e.Mu.Unlock()
}

// Build private graph fixtures through genuine storage and membership candidates.
// Temporary descriptor names/flags exist only on unpublished roots and are restored.
func setFixtureEdges(r *access.RoleRecord, edges []authorization.Membership) {
	candidate := *r
	destination := r
	r = &candidate
	read := r.Root.Read(authorization.PrincipalRef{})
	origins := make(map[authorization.PrincipalRef]bool)
	for edge := range read.Edges() {
		origins[edge.Grantor] = true
	}
	if len(origins) != 0 {
		root, err := authorization.OwnedMembershipCandidate(context.Background(), read, origins)
		if err != nil {
			panic(err)
		}
		r.Root = root
	}
	for _, edge := range edges {
		original := fixtureRole(r, edge.Grantor)
		oldBootstrap, hasBootstrap := r.Root.Read(authorization.PrincipalRef{}).Lookup("postgres")
		if hasBootstrap && oldBootstrap.Ref != original.Ref {
			temp := oldBootstrap
			temp.Name = fmt.Sprintf("fixture_bootstrap_%d", temp.Ref.ID())
			putFixtureRole(r, temp)
		}
		elevated := original
		elevated.Name = "postgres"
		elevated.Superuser = true
		putFixtureRole(r, elevated)
		target := fixtureRole(r, edge.Role)
		member := fixtureRole(r, edge.Member)
		request := &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: target.Name}}, Members: []parser.RoleSpec{{Name: member.Name}}, Options: []parser.MembershipOption{{Name: "admin", Value: edge.Admin}, {Name: "inherit", Value: edge.Inherit}, {Name: "set", Value: edge.Set}}}
		root, changed, _, err := authorization.MembershipCandidate(context.Background(), r.Root.Read(authorization.PrincipalRef{}), authorization.RoleState{CurrentUser: edge.Grantor, SessionUser: edge.Grantor}, request, nil, false, false)
		if err != nil {
			panic(err)
		}
		if changed {
			r.Root = root
		}
		putFixtureRole(r, original)
		if hasBootstrap && oldBootstrap.Ref != original.Ref {
			putFixtureRole(r, oldBootstrap)
		}
	}
	destination.Root = candidate.Root
}

func fixtureEdges(r *access.RoleRecord) []authorization.Membership {
	return r.Root.Read(authorization.PrincipalRef{}).Memberships()
}
