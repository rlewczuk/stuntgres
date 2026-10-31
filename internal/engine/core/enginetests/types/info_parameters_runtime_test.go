package types_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRoutineDefaultPresentation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: dtpUnitAuthority(t, f, view)})
	for _, x := range []struct {
		oid  ids.OID
		slot int
		want string
	}{
		{2096, 1, "0"},
		{3464, 0, "0"}, {3464, 6, "0.0"}, {6212, 0, "0"}, {6212, 1, "1"}, {1268, 1, "true"}, {4005, 2, "'{}'::jsonb"}, {4005, 3, "false"}, {4350, 1, "'NFC'::text"},
		{3782, 3, "'{}'::text[]"}, {3783, 3, "'{}'::text[]"},
		{3784, 3, "'{}'::text[]"}, {3785, 3, "'{}'::text[]"},
	} {
		d := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: x.oid})
		if x.oid == 2096 && (d.Signature != "23,20" || d.LiteralDefaults.At(0) != (catalog.RoutineLiteral{SourceType: 23, Text: "0"})) {
			t.Fatal("default source constant was confused with the bigint formal", d)
		}
		if x.oid == 2096 {
			treeType, _ := view.LookupType(sqltype.PgNodeTree)
			defaults, _, err := runtime.RoutineTrees(t.Context(), view, d, treeType)
			if err != nil {
				t.Fatal(err)
			}
			tree, err := defaults.Tree()
			if err != nil {
				t.Fatal(err)
			}
			list, ok := tree.(plan.ExpressionListTree)
			if !ok || list.Len() != 1 {
				t.Fatal("missing owned default expression", tree)
			}
			coercion, ok := list.At(0).(plan.CoercionExpr)
			if !ok || coercion.ResultUsage().Type.ID() != sqltype.Int8 || coercion.Mode != sqltype.Assignment {
				t.Fatal("default lost its bigint formal coercion", list.At(0))
			}
			literal, ok := coercion.Child.(plan.LiteralExpr)
			if !ok || literal.ResultUsage().Type.ID() != sqltype.Int4 {
				t.Fatal("default lost its released source constant", coercion.Child)
			}
			value, err := literal.Value.Integer()
			if err != nil || value != 0 {
				t.Fatal(value, err)
			}
		}
		got, err := runtime.RoutineDefault(t.Context(), view, d, x.slot)
		if err != nil || got != x.want {
			t.Fatal(x, got, err)
		}
		copied := *d
		if _, err := runtime.RoutineDefault(t.Context(), view, &copied, x.slot); err == nil {
			t.Fatal("copied declaration accepted")
		}
	}
	d := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1268})
	if _, err := runtime.RoutineDefault(t.Context(), engine.New().CatalogRead(), d, 1); err == nil {
		t.Fatal("foreign catalog accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runtime.RoutineDefault(canceled, view, d, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, recipe := range []*catalog.RoutineLiteralDefaults{
		nil, catalog.NewRoutineLiteralDefaults(), catalog.NewRoutineLiteralDefaults(catalog.RoutineLiteral{SourceType: 99999, Text: "1"}), catalog.NewRoutineLiteralDefaults(catalog.RoutineLiteral{SourceType: 16, Text: "bad"}), catalog.NewRoutineLiteralDefaults(catalog.RoutineLiteral{SourceType: 16, Null: true, Text: "bad"}),
	} {
		state := catalog.CloneState(view.State())
		copy := *d
		copy.LiteralDefaults = recipe
		state.SetDescriptor(copy.Address(), &copy)
		captured := f.e.Catalog.Read(state)
		r := core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: dtpUnitAuthority(t, f, captured)})
		if _, err := r.RoutineDefault(t.Context(), captured, &copy, 1); err == nil {
			t.Fatal("malformed recipe accepted", recipe)
		}
	}
	// Real checked SQL default authority uses the presentation deparser, while its
	// independent replay renderer and captured node remain unchanged.
	state := catalog.CloneState(view.State())
	integer, _ := view.LookupType(sqltype.Int4)
	value, _ := sqltype.Integer(integer, 9)
	definition := &proc.Definition{}
	if err := definition.SetFormals([]proc.Formal{{Name: "a", Mode: "i", Usage: integer.Usage(), HasDefault: true}}); err != nil {
		t.Fatal(err)
	}
	sqlRoutine := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 48001}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Signature: "23", SQL: definition}
	state.SetDescriptor(sqlRoutine.Address(), sqlRoutine)
	captured := f.e.Catalog.Read(state)
	node := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}
	defaults, err := stored.NewRoutineDefaults(t.Context(), captured, nil, []expr.Expr{node})
	if err != nil {
		t.Fatal(err)
	}
	sqlRoutine.SQLDefaults = defaults
	r := core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: dtpUnitAuthority(t, f, captured)})
	replay, err := defaults.DefaultSQL(t.Context(), captured, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.RoutineDefault(t.Context(), captured, sqlRoutine, 0)
	if err != nil || got != "9" {
		t.Fatal(got, err)
	}
	if again, err := defaults.DefaultSQL(t.Context(), captured, 0); err != nil || again != replay {
		t.Fatal("replay changed", again, err)
	}
	if _, err := r.RoutineDefault(t.Context(), captured, sqlRoutine, 1); err == nil {
		t.Fatal("absent input default accepted")
	}
	null := catalog.NewRoutineLiteralDefaults(catalog.RoutineLiteral{SourceType: 20, Null: true})
	literalRoutine := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 48002}, Signature: "20", Installed: catalog.InstalledRoutineDeclaration{Defaults: 1}, LiteralDefaults: null}
	next := catalog.CloneState(state)
	next.SetDescriptor(literalRoutine.Address(), literalRoutine)
	nv := f.e.Catalog.Read(next)
	nr := core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: dtpUnitAuthority(t, f, nv)})
	if got, err := nr.RoutineDefault(t.Context(), nv, literalRoutine, 0); err != nil || got != "NULL::bigint" {
		t.Fatal("typed NULL", got, err)
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
	// Install a fully identified graph before transactions branch from this
	// fixture. Candidate-only helpers intentionally leave new edge IDs at zero.
	root, frontier, err := roles.Root.AssignMembershipOIDs(authorization.FirstMembershipOID)
	if err != nil {
		t.Fatal(err)
	}
	roles.Root = root
	e.RoleStore.InstallMembershipFrontierLocked(frontier)
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

func dtpUnitAuthority(t *testing.T, f defaultUnitFixture, catalog catalog.Read) core.AuthorizationView {
	t.Helper()
	f.e.Mu.Lock()
	auth, err := f.e.AuthorizationLocked(f.op)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	auth.Recapture(catalog)
	return auth
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
