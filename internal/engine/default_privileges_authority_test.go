package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestDefaultPrivilegesArrayAuthority(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.actor(f.owner)
	create := func(name string) (AuthorizationView, *catalog.Type, *catalog.Type) {
		t.Helper()
		stage := f.e.BeginCatalogStage(f.e.CatalogRead())
		f.e.Mu.Lock()
		auth, err := f.e.AuthorizationLocked(f.op)
		if err == nil {
			err = typedef.StageCreateEnumLocked(DDLHost{E: f.e, Op: f.op, CatalogStage: stage}, auth, &parser.CreateEnum{
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
	check := func(auth AuthorizationView, base, array *catalog.Type, allowed bool) {
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
		New().Catalog.Domain.Session(f.op.Session.SessionToken.ID()),
		f.e.Catalog.Domain.PrivateSession(f.op.Session),
	} {
		peer := auth
		peer.Session = &session.Session{SessionToken: token}
		defaultUnitDiagnostic(t, peer.CanUse(ref), "42501", "permission denied for schema "+namespace.Name())
	}
	// Even an exact namespace token cannot elevate zero/foreign/private
	// maintenance identities into a physical backend.
	for _, token := range []ids.SessionToken{{}, New().Catalog.Domain.Session(1), f.e.Catalog.Domain.PrivateSession(f.op.Session)} {
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
	foreign = New().Catalog.Domain.Ref(foreign.Epoch, foreign.Address)
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
