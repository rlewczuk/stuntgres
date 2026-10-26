package core_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgAuthIDUnitSource(t *testing.T, e *core.Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.RoleClassID}
	d, ok := e.Catalog.State.Relation(address)
	if !ok {
		t.Fatal("missing pg_authid")
	}
	return d, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
}

func pgAuthIDUnitRows(t *testing.T, source *exec.Source) map[uint32][]sqltype.Value {
	t.Helper()
	if source == nil {
		t.Fatal("missing shadow capture")
	}
	bag := make(map[uint32][]sqltype.Value)
	for _, row := range source.Anonymous {
		if len(row) != 12 {
			t.Fatal("shadow width")
		}
		id, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		if bag[id] != nil {
			t.Fatal("duplicate role identity")
		}
		bag[id] = row
	}
	return bag
}

func TestPGAuthIDDescriptor(t *testing.T) {
	e := engine.New()
	d, _ := pgAuthIDUnitSource(t, e)
	if d.VirtualView || d.CatalogView || d.RelationKind != catalog.CatalogRelation || d.RowType != 2842 || len(d.Columns) != 12 || d.ColumnMeta.HighWater != 12 {
		t.Fatal("authid descriptor")
	}
	names := []string{"oid", "rolname", "rolsuper", "rolinherit", "rolcreaterole", "rolcreatedb", "rolcanlogin", "rolreplication", "rolbypassrls", "rolconnlimit", "rolpassword", "rolvaliduntil"}
	types := []sqltype.ID{26, 19, 16, 16, 16, 16, 16, 16, 16, 23, 25, 1184}
	for i, c := range d.Columns {
		collation := uint32(0)
		if i == 1 || i == 10 {
			collation = 950
		}
		if c.Name != names[i] || c.Usage.Type.ID() != types[i] || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.Nullable != (i >= 10) || !c.NullabilityKnown || c.OriginTable != 1260 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i {
			t.Fatal("authid column", i)
		}
		fact := d.ColumnMeta.CatalogAttributes[i]
		if fact.NotNull != (i < 10) || fact.Collation != collation || fact.Dimensions != 0 {
			t.Fatal("authid attribute", i)
		}
	}
	for _, entry := range d.Security.ACL.Entries {
		if entry.Grantee.Public {
			t.Fatal("PUBLIC authid rights")
		}
	}
	if err := core.RelationProvider.Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ids.OID{2842, 10057} {
		if e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}).OwningRelation != d.Address() {
			t.Fatal("row/array ownership")
		}
	}
	if !pgcatalog.SummaryTable(ids.RoleClassID) || !pgcatalog.CatalogHasIndexes(ids.RoleClassID) || pgcatalog.SummaryViewDefinition(ids.RoleClassID) != "" {
		t.Fatal("real-table classification")
	}
}

func TestPGAuthIDTypedProjectionAndIdentityWidth(t *testing.T) {
	e := engine.New()
	d, ref := pgAuthIDUnitSource(t, e)
	state := emptyRoleFixture(e)
	low := pgUserUnitRole(e, state, 20000, "low")
	pgUserUnitRole(e, state, uint64(1)<<32|20000, "alias")
	max := pgUserUnitRole(e, state, math.MaxUint32, "max")
	pgUserUnitRole(e, state, uint64(math.MaxUint32)+1, "overflow")
	hidden := pgUserUnitRole(e, state, 20001, "hidden")
	r := fixtureRole(state, hidden)
	r.Login = false
	putFixtureRole(state, r)
	r = fixtureRole(state, low)
	r.Password = "arbitrary stored text, not a verifier validator"
	r.CreateDB, r.Superuser, r.Replication, r.BypassRLS = true, true, true, true
	r.Inherit, r.CreateRole = true, false
	r.HasValidUntil, r.ValidUntil, r.ConnectionLimit = true, 0, -7
	putFixtureRole(state, r)
	r = fixtureRole(state, max)
	r.Password = ""
	putFixtureRole(state, r)
	capture := func(root *access.RoleRecord) *exec.Source {
		t.Helper()
		s, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), root), Ref: ref, Source: d, Runtime: core.EngineCatalogRuntime{}}, registeredCatalogProducer(ids.RoleClassID))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if empty := capture(emptyRoleFixture(e)); empty == nil || empty.Anonymous == nil || len(empty.Anonymous) != 0 {
		t.Fatal("empty capture lost anonymous source identity")
	}
	bag := pgAuthIDUnitRows(t, capture(state))
	if len(bag) != 3 || bag[20000] == nil || bag[math.MaxUint32] == nil {
		t.Fatal("all-role/OID width")
	}
	for id, row := range bag {
		for i, value := range row {
			if value.Type() != d.Columns[i].Usage.Type {
				t.Fatal("concrete typed field", i)
			}
		}
		for _, i := range []int{2, 5, 7, 8} {
			flag, err := row[i].Boolean()
			if err != nil || flag != (id == 20000) {
				t.Fatal("direct flags")
			}
		}
		for _, i := range []int{3, 4, 6} {
			want := i == 3 && id == 20000 || i == 6 && id != 20001
			flag, err := row[i].Boolean()
			if err != nil || flag != want {
				t.Fatal("independent direct flag", i)
			}
		}
		wantLimit := int64(0)
		if id == 20000 {
			wantLimit = -7
		}
		if n, err := row[9].Integer(); err != nil || n != wantLimit {
			t.Fatal("signed connection limit")
		}
	}
	if text, _ := bag[20000][10].Text(); text != "arbitrary stored text, not a verifier validator" {
		t.Fatal("stored password changed")
	}
	if bag[20000][11].IsNull() {
		t.Fatal("zero timestamp confused with absence")
	}
	if stamp, _ := bag[20000][11].Timestamp(); stamp != 0 {
		t.Fatal("zero timestamp")
	}
	if !bag[math.MaxUint32][10].IsNull() || !bag[math.MaxUint32][11].IsNull() {
		t.Fatal("missing credentials not NULL")
	}
	for _, stamp := range []int64{123456789, math.MaxInt64, math.MinInt64} {
		updated := cloneRoleFixture(state)
		r = fixtureRole(updated, low)
		r.ValidUntil = stamp
		putFixtureRole(updated, r)
		if got, _ := pgAuthIDUnitRows(t, capture(updated))[20000][11].Timestamp(); got != stamp {
			t.Fatal("timestamp/infinity changed")
		}
	}
	updated := cloneRoleFixture(state)
	r = fixtureRole(updated, low)
	r.Password = "replacement"
	r.Name = "replacement"
	putFixtureRole(updated, r)
	e.RoleStore.PublishLocked(updated)
	if text, _ := bag[20000][10].Text(); text != "arbitrary stored text, not a verifier validator" {
		t.Fatal("retained password mutated")
	}
	retireFixtureRole(updated, low)
	if pgAuthIDUnitRows(t, capture(updated))[20000] != nil {
		t.Fatal("high identity resurrected low")
	}
}

func TestPGAuthIDStatementAndNestedSnapshot(t *testing.T) {
	e := engine.New()
	d, ref := pgAuthIDUnitSource(t, e)
	old := cloneRoleFixture(e.RoleStore.Published)
	role := pgUserUnitRole(e, old, 20000, "snapshot")
	e.RoleStore.PublishLocked(old)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	scope := &core.StatementScope{Op: op, Owner: op.Session.Transactions}
	e.Mu.Lock()
	err := e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	open := func(scope *core.StatementScope, ref ids.ObjectRef) *exec.Source {
		t.Helper()
		e.Mu.Lock()
		s, err := core.EngineOpenRelationSourceLocked(e, scope, ref, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first := open(scope, ref)
	captured := scope.Inquiry.CommentCatalog
	if captured.State() == nil {
		t.Fatal("missing captured catalog backing")
	}
	capturedRoles := core.InquiryDeparseAuth(&scope.Inquiry).RoleRecord
	if capturedRoles != old {
		t.Fatal("capture did not retain published immutable role record")
	}
	updated := cloneRoleFixture(old)
	r := fixtureRole(updated, role)
	r.Password = "new verifier"
	putFixtureRole(updated, r)
	e.RoleStore.PublishLocked(updated)
	second := open(scope, ref)
	if scope.Inquiry.CommentCatalog.State() != captured.State() {
		t.Fatal("statement replaced captured backing")
	}
	if core.InquiryDeparseAuth(&scope.Inquiry).RoleRecord != capturedRoles {
		t.Fatal("statement replaced role authority backing")
	}
	addView := func(id ids.OID, name string, leaf ids.ObjectRef) ids.ObjectRef {
		v := *d
		v.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
		v.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		v.ObjectName = name
		v.RelationKind = catalog.ViewRelation
		v.VirtualView = false
		attrs := append([]int32(nil), d.Attributes...)
		v.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.SourceNode{QuerySchema: plan.QuerySchema(d.Columns), Ref: leaf}}, Sources: []stored.ViewLeaf{{Ref: leaf, Attributes: attrs, Reads: attrs}}})
		e.Catalog.State.SetDescriptor(v.Address(), &v)
		return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), v.Address())
	}
	inner := addView(20010, "shadow_inner", ref)
	outer := addView(20011, "shadow_outer", inner)
	nested := open(scope, outer)
	defer nested.Close()
	if nested.NestedInputs == nil || nested.NestedInputs.NestedInputs == nil {
		t.Fatal("nested capture missing")
	}
	for _, s := range []*exec.Source{first, second, nested.NestedInputs.NestedInputs} {
		if text, _ := pgAuthIDUnitRows(t, s)[20000][10].Text(); text != "unit-private-verifier" {
			t.Fatal("statement resampled credential")
		}
	}
	fresh := &core.StatementScope{Op: op, Owner: op.Session.Transactions}
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(fresh)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := pgAuthIDUnitRows(t, open(fresh, ref))[20000][10].Text(); text != "new verifier" {
		t.Fatal("fresh credential stale")
	}
	if core.InquiryDeparseAuth(&fresh.Inquiry).RoleRecord != updated {
		t.Fatal("fresh scope retained obsolete role backing")
	}
}

func TestPGAuthIDCancellationAndEmptySource(t *testing.T) {
	e := engine.New()
	d, ref := pgAuthIDUnitSource(t, e)
	state := emptyRoleFixture(e)
	pgUserUnitRole(e, state, 20000, "first")
	pgUserUnitRole(e, state, 20001, "second")
	empty, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), emptyRoleFixture(e)), Ref: ref, Source: d, Runtime: core.EngineCatalogRuntime{}}, registeredCatalogProducer(ids.RoleClassID))
	if err != nil || empty == nil || empty.Anonymous == nil || len(empty.Anonymous) != 0 {
		t.Fatal("empty anonymous source identity", err)
	}
	// The producer checks entry, each role, then the completed rowset before
	// publication. Derive the final check from this fixture's actual role root.
	finalCheck := state.Root.Read(authorization.PrincipalRef{}).RoleCount() + 2
	for _, phase := range []struct {
		name string
		at   int
	}{
		{"entry", 1},
		{"first role", 2},
		{"mid iteration", 3},
		{"final publication", finalCheck},
	} {
		t.Run(phase.name, func(t *testing.T) {
			ctx := &pgShDescriptionCancelContext{Context: context.Background(), cancelAt: phase.at}
			source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), state), Ref: ref, Source: d, Runtime: core.EngineCatalogRuntime{}}, registeredCatalogProducer(ids.RoleClassID))
			if source != nil || !errors.Is(err, context.Canceled) || ctx.calls != phase.at {
				t.Fatal("cancellation published credential prefix or missed check", phase.at, ctx.calls, err)
			}
		})
	}
	invalid := *d
	invalid.Columns = append([]catalog.Column(nil), d.Columns...)
	invalid.Columns[0].Usage.Type = enginetest.Usage(e, sqltype.Int4).Type
	source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), state), Ref: ref, Source: &invalid, Runtime: core.EngineCatalogRuntime{}}, registeredCatalogProducer(ids.RoleClassID))
	if err == nil || source != nil {
		t.Fatal("invalid descriptor published rows")
	}
}

func TestPGAuthIDAuthorizationPrincipals(t *testing.T) {
	e := engine.New()
	d, ref := pgAuthIDUnitSource(t, e)
	state := cloneRoleFixture(e.RoleStore.Published)
	reader := pgUserUnitRole(e, state, 20000, "authid_reader")
	// MembershipCandidate defaults a new edge's INHERIT option from its member.
	// Model an ordinary CREATE ROLE (INHERIT), not pgUserUnitRole's zero flags.
	readerRole := fixtureRole(state, reader)
	readerRole.Inherit = true
	putFixtureRole(state, readerRole)
	admin := state.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	e.RoleStore.PublishLocked(state)
	authority := func(principal authorization.PrincipalRef) core.AuthorizationView {
		return e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: e.Catalog.Current(), Catalog: e.Catalog.Current(), RoleRecord: e.RoleStore.Published, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), State: authorization.RoleState{CurrentUser: principal}})
	}
	req := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}
	denied := func(err error) {
		t.Helper()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for table pg_authid" {
			t.Fatal("authid table denial", err)
		}
	}
	auth := authority(reader)
	denied(auth.Check(req))
	if err := authority(admin).Check(req); err != nil {
		t.Fatal("superuser", err)
	}
	name := ref
	name.Address.SubID = 2
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.SelectPrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), name, acl); err != nil {
		t.Fatal(err)
	}
	auth = authority(reader)
	for _, columns := range [][]int{nil, {1}} {
		if err := auth.CheckColumns(ref, d, authorization.SelectPrivilege, columns); err != nil {
			t.Fatal("name/presence delegate", err)
		}
	}
	for _, columns := range [][]int{{10}, {1, 10}} {
		denied(auth.CheckColumns(ref, d, authorization.SelectPrivilege, columns))
	}
	denied(auth.Check(req))
	if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
	auth = authority(reader)
	if err := auth.Check(req); err != nil {
		t.Fatal("relation delegate", err)
	}
	for _, target := range []ids.ObjectRef{ref, name} {
		if err := enginetest.SetObjectACL(e, context.Background(), target, authorization.ACLValue{Explicit: true}); err != nil {
			t.Fatal(err)
		}
	}
	auth = authority(reader)
	denied(auth.Check(req))
	namespace := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Namespace())
	if err := enginetest.SetObjectACL(e, context.Background(), namespace, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	var schemaError *sqlstate.Error
	if err := authority(reader).CanUse(namespace); !errors.As(err, &schemaError) || schemaError.Code != "42501" || schemaError.Message != "permission denied for schema pg_catalog" {
		t.Fatal("schema usage checkpoint", err)
	}
	usage := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.UsagePrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), namespace, usage); err != nil {
		t.Fatal(err)
	}
	if err := authority(reader).CanUse(namespace); err != nil {
		t.Fatal("real schema usage delegate", err)
	}
	membership := func(target string, revoke bool) {
		t.Helper()
		next := cloneRoleFixture(e.RoleStore.Published)
		root, changed, _, err := authorization.MembershipCandidate(context.Background(), next.Root.Read(authorization.PrincipalRef{}), authorization.RoleState{CurrentUser: admin}, &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: target}}, Members: []parser.RoleSpec{{Name: "authid_reader"}}, Revoke: revoke}, nil, false, false)
		if err != nil || !changed {
			t.Fatal("membership publication", err)
		}
		next.Root = root
		e.RoleStore.PublishLocked(next)
	}
	membership("pg_read_all_data", false)
	if err := authority(reader).Check(req); err != nil {
		t.Fatal("broad read", err)
	}
	membership("pg_read_all_data", true)
	denied(authority(reader).Check(req))
	membership("postgres", false)
	// Owner membership supplies ownership, not ordinary SELECT after explicit
	// revocation. Restore the actual seeded owner ACL before testing its rights.
	denied(authority(reader).Check(req))
	if err := authority(reader).Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		t.Fatal("inherited ownership despite revoked SELECT", err)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), ref, d.Security.ACL); err != nil {
		t.Fatal(err)
	}
	if err := authority(reader).Check(req); err != nil {
		t.Fatal("inherited owner", err)
	}
	if role, err := authority(reader).Roles.Descriptor(reader); err != nil || role.Superuser {
		t.Fatal("inherited direct superuser flag")
	}
	membership("postgres", true)
	denied(authority(reader).Check(req))
	for _, bad := range []ids.ObjectRef{func() ids.ObjectRef { v := ref; v.Epoch++; return v }(), engine.New().Catalog.Domain.Ref(ref.Epoch, ref.Address)} {
		if err := authority(admin).Check(access.Requirement{Object: bad, Privileges: authorization.SelectPrivilege}); err == nil {
			t.Fatal("invalid object bypassed admission")
		}
	}
	if err := enginetest.SetObjectACL(e, context.Background(), name, acl); err != nil {
		t.Fatal(err)
	}
	next := cloneRoleFixture(e.RoleStore.Published)
	retireFixtureRole(next, reader)
	replacement := pgUserUnitRole(e, next, 30000, "authid_reader")
	e.RoleStore.PublishLocked(next)
	if err := authority(reader).Check(req); err == nil {
		t.Fatal("retired principal accepted")
	}
	if err := authority(replacement).CheckColumns(ref, d, authorization.SelectPrivilege, []int{1}); err == nil {
		t.Fatal("same-name principal inherited old ACL")
	}
	foreign := engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	if err := authority(foreign).Check(req); err == nil {
		t.Fatal("foreign superuser accepted")
	}
}
