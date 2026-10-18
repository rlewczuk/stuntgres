package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestDefaultPrivilegesArrayAuthority(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.actor(f.owner)
	create := func(name string) (AuthorizationView, *typeDescriptor, *typeDescriptor) {
		t.Helper()
		stage := f.e.BeginCatalogStage(f.e.CatalogRead())
		f.e.mu.Lock()
		auth, err := f.e.authorizationLocked(f.op)
		if err == nil {
			err = f.e.stageCreateEnumLocked(f.op, auth, stage, &parser.CreateEnum{
				Name:   parser.ObjectName{Parts: []parser.Identifier{{Text: "default_schema"}, {Text: name}}},
				Labels: []parser.EnumLabel{{Text: "a"}},
			})
		}
		if err == nil {
			auth.catalog = f.e.stageRead(stage)
			auth.recaptureCatalog(auth.catalog)
		}
		f.e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		base := auth.catalog.state.descriptors[auth.catalog.state.names[NameKey{ClassID: TypeClassID, Namespace: f.schema, Name: name}]].(*typeDescriptor)
		array := auth.catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(base.array)}].(*typeDescriptor)
		if !authorization.MatchesDefaultBaseline(array.acl, f.owner, authorization.DefaultTypes, false) || descriptorRoleDependencies(array, f.reader, nil) {
			t.Fatal("associated enum array consumed selected TYPES ACL", array.acl)
		}
		auth.state.CurrentUser = f.reader
		return auth, base, array
	}
	check := func(auth AuthorizationView, base, array *typeDescriptor, allowed bool) {
		t.Helper()
		for _, descriptor := range []*typeDescriptor{base, array} {
			ref := ObjectRef{owner: f.e.domain, Epoch: auth.catalog.Epoch(), Address: descriptor.Address()}
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
	if !descriptorRoleDependencies(scopedBase, f.reader, nil) {
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
	f.schema = f.op.session.temporaryNamespace
	state := cloneCatalog(f.e.state)
	namespace := *state.descriptors[f.schema].(*namespaceDescriptor)
	assertTemporaryNamespacePair(t, f.e, f.op.session.token, f.schema)
	namespace.acl = authorization.ACLValue{Explicit: true}
	state.descriptors[f.schema] = &namespace
	f.e.mu.Lock()
	auth, err := f.e.authorizationLocked(f.op)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	auth.catalog = CatalogRead{f.e, state}
	auth.recaptureCatalog(auth.catalog)
	auth.state.CurrentUser = f.reader
	ref := ObjectRef{owner: f.e.domain, Epoch: state.epoch, Address: f.schema}
	for _, privilege := range []authorization.Privileges{authorization.UsagePrivilege, authorization.CreatePrivilege, authorization.UsagePrivilege | authorization.CreatePrivilege} {
		if err := auth.Check(AccessRequirement{Object: ref, Privileges: privilege}); err != nil {
			t.Fatal("physical namespace authority depended on current actor", privilege, err)
		}
	}
	if rights, err := auth.Effective(ref, true); err != nil || rights != 0 {
		t.Fatal("physical authority invented namespace delegation", rights, err)
	}
	defaultUnitDiagnostic(t, auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}), "42501", "must be owner of schema "+namespace.name)
	for _, token := range []SessionToken{
		{},
		{owner: f.e, id: f.op.session.token.id + 1},
		{owner: New(), id: f.op.session.token.id},
		{owner: f.e, private: f.op.session},
	} {
		peer := auth
		peer.session = &Session{token: token}
		defaultUnitDiagnostic(t, peer.CanUse(ref), "42501", "permission denied for schema "+namespace.name)
	}
	// Even an exact namespace token cannot elevate zero/foreign/private
	// maintenance identities into a physical backend.
	for _, token := range []SessionToken{{}, {owner: New(), id: 1}, {owner: f.e, private: f.op.session}} {
		candidate := cloneCatalog(state)
		invalidNamespace := namespace
		invalidNamespace.temporary = token
		candidate.descriptors[f.schema] = &invalidNamespace
		invalid := auth
		invalid.catalog = CatalogRead{f.e, candidate}
		invalid.recaptureCatalog(invalid.catalog)
		invalid.session = &Session{token: token}
		defaultUnitDiagnostic(t, invalid.CanUse(ref), "42501", "permission denied for schema "+namespace.name)
	}
	withoutSession := auth
	withoutSession.session = nil
	defaultUnitDiagnostic(t, withoutSession.CanUse(ref), "42501", "permission denied for schema "+namespace.name)
	ordinary := cloneCatalog(state)
	permanent := namespace
	permanent.temporary = SessionToken{}
	ordinary.descriptors[f.schema] = &permanent
	ordinaryAuth := auth
	ordinaryAuth.catalog = CatalogRead{f.e, ordinary}
	ordinaryAuth.recaptureCatalog(ordinaryAuth.catalog)
	defaultUnitDiagnostic(t, ordinaryAuth.CanUse(ref), "42501", "permission denied for schema "+namespace.name)
	foreign := ref
	foreign.owner = New().domain
	if err := auth.CanUse(foreign); err == nil {
		t.Fatal("physical namespace authority admitted foreign object")
	}
	staleActor := auth
	staleActor.state.CurrentUser = f.e.authorizationDomain.Principal(999999)
	if err := staleActor.CanUse(ref); err == nil {
		t.Fatal("physical namespace authority admitted stale actor")
	}
	table := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 20111}, namespace: f.schema, name: "temp_acl_denied", owner: f.owner, revision: 1, relationKind: tableRelation, acl: authorization.ACLValue{Explicit: true}}
	state.descriptors[table.address] = table
	tableRef := ObjectRef{owner: f.e.domain, Epoch: state.epoch, Address: table.address}
	defaultUnitDiagnostic(t, auth.Check(AccessRequirement{Object: tableRef, Privileges: authorization.SelectPrivilege}), "42501", "permission denied for table temp_acl_denied")
}
