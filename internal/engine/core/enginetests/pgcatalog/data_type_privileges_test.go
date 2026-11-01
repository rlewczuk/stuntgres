package pgcatalog_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

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

func dtpUnitCapture(ctx context.Context, catalog catalog.Read, auth core.AuthorizationView) (*exec.Source, error) {
	source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.DataTypePrivilegesOID})
	ref := catalog.Owner().Domain.Ref(catalog.Epoch(), source.Address())
	return catalogrt.CaptureRows(pgcatalog.Input{Context: ctx, Catalog: catalog, Ref: ref, Source: source, Roles: (auth).Roles, Owner: (auth).Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Temporary: core.CatalogSourceTemporaryOwner(auth), Database: "unit_database"})}, captureDataTypePrivileges)
}

func dtpUnitText(t *testing.T, value sqltype.Value) string {
	t.Helper()
	if value.Type() == nil || !value.Type().IsDomain() {
		t.Fatal("missing source domain", value)
	}
	base, err := value.BaseValue()
	if err != nil {
		t.Fatal("source domain payload", err)
	}
	text, err := base.Text()
	if err != nil {
		t.Fatal("source text payload", err)
	}
	return text
}

// A deterministic cancellation point inside enumeration, without timing races.
type dtpCancelContext struct {
	context.Context
	remaining int
}

func (c *dtpCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestDataTypePrivilegesCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	auth := dtpUnitAuthority(t, f, view)
	for _, ctx := range []context.Context{&dtpCancelContext{Context: context.Background(), remaining: 1}, &dtpCancelContext{Context: context.Background(), remaining: 4}} {
		rows, err := dtpUnitCapture(ctx, view, auth)
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed rows", rows, err)
		}
	}
	for _, change := range []func(*core.AuthorizationView){
		func(a *core.AuthorizationView) { a.Owner = &engine.New().Catalog.Owner },
		func(a *core.AuthorizationView) { a.RoleRecord = nil },
		func(a *core.AuthorizationView) { a.PairedCatalog = catalog.Read{} },
		func(a *core.AuthorizationView) {
			a.Roles = engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		},
		func(a *core.AuthorizationView) { a.State.CurrentUser = authorization.PrincipalRef{} },
	} {
		bad := auth
		change(&bad)
		if rows, err := dtpUnitCapture(context.Background(), view, bad); rows != nil || err == nil {
			t.Fatal("invalid authority accepted", rows, err)
		}
	}
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.DataTypePrivilegesOID})
	ref := f.e.Catalog.Domain.Ref(view.Epoch(), source.Address())
	stale := ref
	stale.Epoch++
	if rows, err := catalogrt.CaptureRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: stale, Source: source, Roles: (auth).Roles, Owner: (auth).Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Temporary: core.CatalogSourceTemporaryOwner(auth), Database: "unit_database"})}, captureDataTypePrivileges); rows != nil || err == nil {
		t.Fatal("stale ref accepted", rows, err)
	}
	copySource := *source
	if rows, err := catalogrt.CaptureRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: &copySource, Roles: (auth).Roles, Owner: (auth).Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Temporary: core.CatalogSourceTemporaryOwner(auth), Database: "unit_database"})}, captureDataTypePrivileges); rows != nil || err == nil {
		t.Fatal("foreign source accepted", rows, err)
	}
	for i, column := range source.Columns {
		want := sqltype.Name
		if i == 3 {
			want = sqltype.VarChar
		}
		domainID := sqltype.ID(13248)
		if i == 3 {
			domainID = 13246
		}
		if column.Usage.Type.ID() != domainID || !column.Usage.Type.IsDomain() || column.Usage.Type.BaseType().ID() != want || column.Usage.Collation.ID != 950 || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 || column.OriginTable != uint32(pgcatalog.DataTypePrivilegesOID) || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) || !column.Nullable || !column.NullabilityKnown {
			t.Fatal("incorrect source carrier/origin", i, column)
		}
	}
	// An ownerless domain cannot be disclosed under fabricated authority.
	enginetest.RoleExecute(t, f.e, f.op, "CREATE DOMAIN public.dtp_ownerless AS integer")
	state := catalog.CloneState(f.e.Catalog.State)
	domain := *domainUnitType(t, f.e.Catalog.Read(state), "dtp_ownerless")
	domain.Owner = authorization.PrincipalRef{}
	state.SetDescriptor(domain.Address(), &domain)
	badCatalog := f.e.Catalog.Read(state)
	if rows, err := dtpUnitCapture(context.Background(), badCatalog, dtpUnitAuthority(t, f, badCatalog)); rows != nil || err == nil {
		t.Fatal("ownerless user domain disclosed", rows, err)
	}
}

func TestDataTypePrivilegesVisibility(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	typ, _ := (f.e.Catalog.Read(state)).LookupType(sqltype.Int4)
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20110}, ObjectNamespace: f.schema, ObjectName: "dtp_unit", Owner: f.owner, ObjectRevision: 1, RelationKind: catalog.TableRelation, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}, ColumnACL: map[int32]authorization.ACLValue{3: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}}}, Attributes: []int32{1, 3, 4}, Columns: []catalog.Column{{Name: "a", Usage: typ.Usage()}, {Name: "c", Usage: typ.Usage()}, {Name: "d", Usage: typ.Usage()}}}
	state.SetDescriptor(relation.Address(), relation)
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = f.reader
	held, err := dtpUnitCapture(context.Background(), view, auth)
	if err != nil {
		t.Fatal(err)
	}
	selectIDs := func(source *exec.Source) []string {
		var ids []string
		for _, row := range source.Anonymous {
			name := dtpUnitText(t, row[2])
			if name == "dtp_unit" {
				id := dtpUnitText(t, row[4])
				ids = append(ids, id)
			}
		}
		return ids
	}
	if got := selectIDs(held); !reflect.DeepEqual(got, []string{"3"}) {
		t.Fatal("stable column-only identity", got)
	}
	before := make([][]sqltype.Value, len(held.Anonymous))
	for i, row := range held.Anonymous {
		before[i] = append([]sqltype.Value(nil), row...)
	}
	changed := catalog.CloneState(state)
	revoked := *relation
	revoked.Security.ColumnACL = nil
	changed.SetDescriptor(relation.Address(), &revoked)
	auth.Recapture(f.e.Catalog.Read(changed))
	fresh, err := dtpUnitCapture(context.Background(), auth.Catalog, auth)
	if err != nil || len(selectIDs(fresh)) != 0 || !reflect.DeepEqual(before, held.Anonymous) {
		t.Fatal("capture lifetime", err)
	}
	auth.State.CurrentUser = f.owner
	owned, err := dtpUnitCapture(context.Background(), auth.Catalog, auth)
	if err != nil || !reflect.DeepEqual(selectIDs(owned), []string{"1", "3", "4"}) {
		t.Fatal("owner independent of ordinary ACL", err)
	}
	auth.State.CurrentUser = f.member
	inherited, err := dtpUnitCapture(context.Background(), auth.Catalog, auth)
	if err != nil || !reflect.DeepEqual(selectIDs(inherited), []string{"1", "3", "4"}) {
		t.Fatal("inherited owner", err)
	}
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

// Unit tests name producers by their former engine identifiers. Each one is
// resolved through the real pgcatalog registry, so a missing registration
// fails at package initialization instead of silently testing another path.
var (
	captureAvailableExtensions = registeredCatalogProducer(12081)
	captureDataTypePrivileges  = registeredCatalogProducer(16201)
	captureExtensionCatalog    = registeredCatalogProducer(1255)
	capturePGAM                = registeredCatalogProducer(2601)
	capturePGAggregate         = registeredCatalogProducer(2600)
	capturePGAttribute         = registeredCatalogProducer(1249)
	capturePGCast              = registeredCatalogProducer(2605)
	capturePGCollation         = registeredCatalogProducer(3456)
	capturePGConstraint        = registeredCatalogProducer(2606)
	capturePGCursors           = registeredCatalogProducer(12077)
	capturePGDatabase          = registeredCatalogProducer(1262)
	capturePGDefaultACL        = registeredCatalogProducer(826)
	capturePGDepend            = registeredCatalogProducer(2608)
	capturePGDescription       = registeredCatalogProducer(2609)
	capturePGEnum              = registeredCatalogProducer(3501)
	capturePGGroup             = registeredCatalogProducer(12008)
	capturePGIndexes           = registeredCatalogProducer(12002)
	capturePGNamespace         = registeredCatalogProducer(2615)
	capturePGOpclass           = registeredCatalogProducer(2616)
	capturePGOperator          = registeredCatalogProducer(2617)
	capturePGParameterACL      = registeredCatalogProducer(6243)
	capturePGRange             = registeredCatalogProducer(3541)
	capturePGRoles             = registeredCatalogProducer(12007)
	capturePGSequence          = registeredCatalogProducer(2224)
	capturePGSettings          = registeredCatalogProducer(16202)
	capturePGShDescription     = registeredCatalogProducer(2396)
	capturePGTablespace        = registeredCatalogProducer(1213)
	capturePGType              = registeredCatalogProducer(1247)
	capturePGUser              = registeredCatalogProducer(12014)
	capturePreparedSource      = registeredCatalogProducer(12001)
	captureTablePrivileges     = registeredCatalogProducer(16200)
	captureTableViewSummary    = registeredCatalogProducer(12000)
	captureTimezoneSource      = registeredCatalogProducer(12004)
)

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

func domainUnitType(t *testing.T, view catalog.Read, name string) *catalog.Type {
	t.Helper()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if len(addresses) != 1 {
		t.Fatal(name, addresses)
	}
	return view.MustType(addresses[0])
}

func defaultUnitReaderEntry(f defaultUnitFixture, privileges, options authorization.Privileges) authorization.ACLEntry {
	return authorization.ACLEntry{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader}, Privileges: privileges, GrantOptions: options}
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

func registeredCatalogProducer(oid ids.OID) pgcatalog.Producer {
	produce, ok := pgcatalog.ProducerFor(oid)
	if !ok {
		panic(fmt.Sprintf("no catalog producer registered for OID %d", oid))
	}
	return produce
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

func fixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) authorization.RoleDescriptor {
	d, _ := r.Root.Read(authorization.PrincipalRef{}).Find(ref)
	return d
}
