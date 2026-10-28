package enginetests_test

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
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func TestCatalogSourceRuntimeViewReferencesConsumer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	target := sequence.NewRelation(sequence.RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20300}, Namespace: f.schema, Name: "runtime_target", Revision: 1, Owner: f.owner, Columns: sequence.Columns(f.e.CatalogRead())})
	target.RelationKind = catalog.TableRelation
	target.TableMethod = 2
	state.SetDescriptor(target.Address(), target)
	ref := f.e.Catalog.Domain.Ref(state.Epoch(), target.Address())
	consumer := *target
	consumer.ObjectAddress.ObjectID++
	consumer.ObjectName = "runtime_consumer"
	consumer.RelationKind = catalog.ViewRelation
	consumer.View = stored.NewView(stored.ViewParts{
		Query:        &plan.Query{Root: &plan.SourceNode{Ref: ref}},
		Sources:      []stored.ViewLeaf{{Ref: ref}},
		Dependencies: []catalog.ExpressionDependency{{Ref: ref, Revision: 1}},
	})
	state.SetDescriptor(consumer.Address(), &consumer)
	view := f.e.Catalog.Read(state)
	copied := consumer
	missing := consumer
	missing.ObjectAddress.ObjectID++
	missing.ObjectName = "runtime_missing_definition"
	missing.View = nil
	typedNil := missing
	typedNil.ObjectAddress.ObjectID++
	typedNil.ObjectName = "runtime_typed_nil_definition"
	typedNil.View = (*stored.View)(nil)
	invalidState := catalog.CloneState(state)
	invalidState.SetDescriptor(missing.Address(), &missing)
	invalidState.SetDescriptor(typedNil.Address(), &typedNil)
	invalidView := f.e.Catalog.Read(invalidState)

	// Both snapshots have the same domain/epoch and valid reference targets.
	// A consumer live only in the earlier snapshot must not borrow the newer
	// snapshot's authority, even though every manifest reference is valid there.
	nextState := catalog.CloneState(state)
	replacement := consumer
	replacement.ObjectName = "runtime_replacement"
	nextState.SetDescriptor(replacement.Address(), &replacement)
	nextView := f.e.Catalog.Read(nextState)
	if ref.Epoch != nextView.Epoch() || !stored.ViewReferenceValid(nextView, &consumer, ref) {
		t.Fatal("borrowed-consumer fixture lacks a current-catalog-valid manifest")
	}

	// pg_depend calls this seam without an authorization capture. The explicit
	// catalog, not runtime.auth.Catalog, owns the descriptor/definition frontier.
	runtime := core.EngineCatalogRuntime{}
	for _, tc := range []struct {
		name     string
		view     catalog.Read
		consumer *catalog.Relation
		valid    bool
	}{
		{"nil_relation", view, nil, false},
		{"missing_definition", invalidView, &missing, false},
		{"typed_nil_definition", invalidView, &typedNil, false},
		{"copied_descriptor", view, &copied, false},
		{"borrowed_consumer", nextView, &consumer, false},
		{"live_consumer", nextView, &replacement, true},
		{"captured_catalog", view, &consumer, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Report invalid-input panics as regression failures without hiding
			// the remaining independently selected consumer-boundary cases.
			defer func() {
				if failure := recover(); failure != nil {
					t.Errorf("ViewReferences panicked instead of returning an error: %v", failure)
				}
			}()
			refs, err := runtime.ViewReferences(t.Context(), tc.view, tc.consumer)
			if tc.valid {
				if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{target.Address()}) {
					t.Fatal("valid live definition lost direct references", refs, err)
				}
			} else if refs != nil || !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("invalid consumer accepted or returned a reference prefix", refs, err)
			}
		})
		t.Run(tc.name+"_canceled", func(t *testing.T) {
			defer func() {
				if failure := recover(); failure != nil {
					t.Errorf("ViewReferences panicked instead of honoring cancellation: %v", failure)
				}
			}()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if refs, err := runtime.ViewReferences(ctx, tc.view, tc.consumer); refs != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation did not precede consumer validation", refs, err)
			}
		})
	}
}

func TestCatalogSourceRuntimeViewReferencesRetainedTemporary(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE TEMP TABLE runtime_retained_base(v INT)")
	enginetest.SessionSQL(t, owner, "CREATE TEMP VIEW runtime_retained_view AS SELECT v FROM runtime_retained_base")
	before := e.CatalogRead()
	address := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: "runtime_retained_view"})
	consumer := before.MustRelation(address)
	target := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: "runtime_retained_base"})
	target.SubID = 1
	for _, tc := range []struct {
		name    string
		replace func() error
	}{
		{"reset", func() error { return engine.Reset(t.Context(), e) }},
		{"restore", func() error { return engine.Restore(t.Context(), e, strings.NewReader("CREATE TABLE runtime_marker(v INT);")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.replace(); err != nil {
				t.Fatal(err)
			}
			current := e.CatalogRead()
			if current.Get(address) != consumer || current.Epoch() <= before.Epoch() {
				t.Fatal("fixture did not retain the exact old-epoch TEMP consumer")
			}
			definition := stored.ViewOf(consumer)
			if len(definition.Sources()) != 1 || definition.Sources()[0].Ref.Epoch != before.Epoch() {
				t.Fatal("fixture lost the retained source manifest")
			}
			refs, err := (core.EngineCatalogRuntime{}).ViewReferences(t.Context(), current, consumer)
			if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{target}) {
				t.Fatal("retained TEMP definition lost direct references", refs, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if refs, err := (core.EngineCatalogRuntime{}).ViewReferences(ctx, current, consumer); refs != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("retained TEMP cancellation exposed references", refs, err)
			}
		})
	}
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
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	return defaultUnitFixture{e: e, op: op, owner: refs[0], reader: refs[1], member: refs[2], schema: schema}
}

type defaultUnitFixture struct {
	e                     *core.Engine
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

func cloneRoleFixture(r *access.RoleRecord) *access.RoleRecord {
	return &access.RoleRecord{Root: r.Root, Revision: r.Revision + 1}
}

func putFixtureRole(r *access.RoleRecord, d authorization.RoleDescriptor) {
	root, err := r.Root.PutDescriptor(d)
	if err != nil {
		panic(err)
	}
	r.Root = root
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

func fixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) authorization.RoleDescriptor {
	d, _ := r.Root.Read(authorization.PrincipalRef{}).Find(ref)
	return d
}
