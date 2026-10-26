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
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgShadowUnitSource(t *testing.T, e *core.Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ShadowOID}
	d, ok := e.Catalog.State.Relation(address)
	if !ok { t.Fatal("missing pg_shadow") }
	return d, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
}

func pgShadowUnitRows(t *testing.T, source *exec.Source) map[uint32][]sqltype.Value {
	t.Helper()
	if source == nil { t.Fatal("missing shadow capture") }
	bag := make(map[uint32][]sqltype.Value)
	for _, row := range source.Anonymous {
		if len(row) != 9 { t.Fatal("shadow width") }
		id, err := row[1].OID()
		if err != nil { t.Fatal(err) }
		if bag[id] != nil { t.Fatal("duplicate role identity") }
		bag[id] = row
	}
	return bag
}

func TestPGShadowDescriptor(t *testing.T) {
	e := engine.New()
	d, _ := pgShadowUnitSource(t, e)
	if !d.VirtualView || !d.CatalogView || d.RelationKind != catalog.CatalogRelation || d.RowType != 12007 || len(d.Columns) != 9 || d.ColumnMeta.HighWater != 9 { t.Fatal("shadow descriptor") }
	names := []string{"usename", "usesysid", "usecreatedb", "usesuper", "userepl", "usebypassrls", "passwd", "valuntil", "useconfig"}
	types := []sqltype.ID{19, 26, 16, 16, 16, 16, 25, 1184, 1009}
	collations := []uint32{950, 0, 0, 0, 0, 0, 950, 0, 950}
	for i, c := range d.Columns {
		if c.Name != names[i] || c.Usage.Type.ID() != types[i] || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collations[i] || !c.Nullable || !c.NullabilityKnown || c.OriginTable != uint32(pgcatalog.ShadowOID) || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i { t.Fatal("shadow column", i) }
	}
	for _, entry := range d.Security.ACL.Entries { if entry.Grantee.Public { t.Fatal("PUBLIC shadow rights") } }
	if err := core.RelationProvider.Validate(e.CatalogRead(), d); err != nil { t.Fatal(err) }
	invalid := *d
	invalid.RelationKind = catalog.TableRelation
	if err := core.RelationProvider.Validate(e.CatalogRead(), &invalid); err == nil { t.Fatal("virtual physical table accepted") }
	invalid = *d
	invalid.Columns = append([]catalog.Column(nil), d.Columns...)
	foreign, _ := engine.New().CatalogRead().LookupType(sqltype.Timestamptz)
	invalid.Columns[7].Usage.Type = foreign
	if err := core.RelationProvider.Validate(e.CatalogRead(), &invalid); err == nil { t.Fatal("foreign timestamp generation accepted") }
	for _, id := range []ids.OID{12007, 12006} {
		typ := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
		if typ.OwningRelation != d.Address() { t.Fatal("shadow row/array ownership") }
	}
	if pgcatalog.SummaryTable(pgcatalog.ShadowOID) || pgcatalog.SummaryViewDefinition(pgcatalog.ShadowOID) == "" { t.Fatal("shadow summary") }
}

func TestPGShadowTypedProjectionAndIdentityWidth(t *testing.T) {
	e := engine.New()
	d, ref := pgShadowUnitSource(t, e)
	state := emptyRoleFixture(e)
	low := pgUserUnitRole(e, state, 20000, "low")
	pgUserUnitRole(e, state, uint64(1)<<32|20000, "alias")
	max := pgUserUnitRole(e, state, math.MaxUint32, "max")
	pgUserUnitRole(e, state, uint64(math.MaxUint32)+1, "overflow")
	hidden := pgUserUnitRole(e, state, 20001, "hidden")
	r := fixtureRole(state, hidden); r.Login = false; putFixtureRole(state, r)
	r = fixtureRole(state, low)
	r.Password = "arbitrary stored text, not a verifier validator"
	r.CreateDB, r.Superuser, r.Replication, r.BypassRLS = true, true, true, true
	r.HasValidUntil, r.ValidUntil, r.ConnectionLimit = true, 0, 0
	putFixtureRole(state, r)
	r = fixtureRole(state, max); r.Password = ""; putFixtureRole(state, r)
	capture := func(root *access.RoleRecord) *exec.Source {
		t.Helper()
		s, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), root), Ref: ref, Source: d, Runtime: core.EngineCatalogRuntime{}}, registeredCatalogProducer(pgcatalog.ShadowOID))
		if err != nil { t.Fatal(err) }; return s
	}
	if empty := capture(emptyRoleFixture(e)); empty == nil || empty.Anonymous == nil || len(empty.Anonymous) != 0 { t.Fatal("empty capture lost anonymous source identity") }
	bag := pgShadowUnitRows(t, capture(state))
	if len(bag) != 2 || bag[20000] == nil || bag[math.MaxUint32] == nil { t.Fatal("LOGIN/OID width") }
	for id, row := range bag {
		for i, value := range row { if value.Type() != d.Columns[i].Usage.Type { t.Fatal("concrete typed field", i) } }
		for i := 2; i <= 5; i++ { flag, err := row[i].Boolean(); if err != nil || flag != (id == 20000) { t.Fatal("direct flags") } }
		if !row[8].IsNull() { t.Fatal("invented configuration") }
	}
	if text, _ := bag[20000][6].Text(); text != "arbitrary stored text, not a verifier validator" { t.Fatal("stored password changed") }
	if bag[20000][7].IsNull() { t.Fatal("zero timestamp confused with absence") }
	if stamp, _ := bag[20000][7].Timestamp(); stamp != 0 { t.Fatal("zero timestamp") }
	if !bag[math.MaxUint32][6].IsNull() || !bag[math.MaxUint32][7].IsNull() { t.Fatal("missing credentials not NULL") }
	for _, stamp := range []int64{123456789, math.MaxInt64, math.MinInt64} {
		updated := cloneRoleFixture(state); r = fixtureRole(updated, low); r.ValidUntil = stamp; putFixtureRole(updated, r)
		if got, _ := pgShadowUnitRows(t, capture(updated))[20000][7].Timestamp(); got != stamp { t.Fatal("timestamp/infinity changed") }
	}
	updated := cloneRoleFixture(state); r = fixtureRole(updated, low); r.Password = "replacement"; r.Name = "replacement"; putFixtureRole(updated, r); e.RoleStore.PublishLocked(updated)
	if text, _ := bag[20000][6].Text(); text != "arbitrary stored text, not a verifier validator" { t.Fatal("retained password mutated") }
	retireFixtureRole(updated, low)
	if pgShadowUnitRows(t, capture(updated))[20000] != nil { t.Fatal("high identity resurrected low") }
}

func TestPGShadowAuthorizationPrincipals(t *testing.T) {
	e := engine.New()
	d, ref := pgShadowUnitSource(t, e)
	op, done := enginetest.RoleOperation(t, e, "postgres"); defer done()
	enginetest.RoleExecute(t, e, op, "CREATE ROLE shadow_reader")
	reader, _ := e.Roles().Lookup("shadow_reader")
	enginetest.RoleExecute(t, e, op, "SET ROLE shadow_reader")
	auth, err := core.OperationAuthorization(op); if err != nil { t.Fatal(err) }
	req := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}
	if err := auth.Check(req); err == nil { t.Fatal("ordinary relation access") }
	for _, ownerOnly := range []bool{false, true} {
		requirement := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege, OwnerOnly: ownerOnly}
		if ownerOnly {
			requirement.Privileges = 0
		}
		err := auth.Check(requirement)
		var diagnostic *sqlstate.Error
		want := "permission denied for view pg_shadow"
		if ownerOnly { want = "must be owner of view pg_shadow" }
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != want { t.Fatal("virtual view diagnostic", err) }
	}
	classRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	if err := enginetest.SetObjectACL(e, context.Background(), classRef, authorization.ACLValue{Explicit: true}); err != nil { t.Fatal(err) }
	auth, err = core.OperationAuthorization(op); if err != nil { t.Fatal(err) }
	var tableDiagnostic *sqlstate.Error
	if err := auth.Check(access.Requirement{Object: classRef, Privileges: authorization.SelectPrivilege}); !errors.As(err, &tableDiagnostic) || tableDiagnostic.Code != "42501" || tableDiagnostic.Message != "permission denied for table pg_class" { t.Fatal("catalog table diagnostic changed", err) }
	staleRef := ref
	staleRef.Epoch++
	if err := auth.Check(access.Requirement{Object: staleRef, Privileges: authorization.SelectPrivilege}); err == nil { t.Fatal("stale catalog epoch accepted") }
	column := ref; column.Address.SubID = 7
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), column, acl); err != nil { t.Fatal(err) }
	auth, err = core.OperationAuthorization(op); if err != nil { t.Fatal(err) }
	if err := auth.Check(access.Requirement{Object: column, Privileges: authorization.SelectPrivilege}); err != nil { t.Fatal("real column delegate", err) }
	if err := auth.CheckColumns(ref, d, authorization.SelectPrivilege, nil); err != nil { t.Fatal("empty manifest must accept real any-column delegation", err) }
	if err := auth.CheckColumns(ref, d, authorization.SelectPrivilege, []int{6}); err != nil { t.Fatal("password column manifest", err) }
	if err := auth.CheckColumns(ref, d, authorization.SelectPrivilege, []int{0, 6}); err == nil { t.Fatal("mixed manifest widened column grant") }
	other := ref; other.Address.SubID = 1
	if err := auth.Check(access.Requirement{Object: other, Privileges: authorization.SelectPrivilege}); err == nil { t.Fatal("column grant widened") }
	if err := auth.Check(req); err == nil { t.Fatal("column grant became relation grant") }
	foreign := engine.New().Catalog.Domain.Ref(ref.Epoch, ref.Address)
	if err := auth.Check(access.Requirement{Object: foreign, Privileges: authorization.SelectPrivilege}); err == nil { t.Fatal("foreign domain accepted") }
	if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil { t.Fatal(err) }
	auth, err = core.OperationAuthorization(op); if err != nil { t.Fatal(err) }
	if err := auth.Check(req); err != nil { t.Fatal("real relation delegate", err) }
	enginetest.RoleExecute(t, e, op, "RESET ROLE")
	enginetest.RoleExecute(t, e, op, "GRANT pg_read_all_data TO shadow_reader")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil { t.Fatal(err) }
	enginetest.RoleExecute(t, e, op, "SET ROLE shadow_reader")
	auth, err = core.OperationAuthorization(op); if err != nil { t.Fatal(err) }
	if err := auth.Check(req); err != nil { t.Fatal("predefined delegate", err) }
	enginetest.RoleExecute(t, e, op, "RESET ROLE")
	enginetest.RoleExecute(t, e, op, "REVOKE pg_read_all_data FROM shadow_reader")
	enginetest.RoleExecute(t, e, op, "GRANT postgres TO shadow_reader")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{}); err != nil { t.Fatal(err) }
	enginetest.RoleExecute(t, e, op, "SET ROLE shadow_reader")
	auth, err = core.OperationAuthorization(op); if err != nil { t.Fatal(err) }
	if err := auth.Check(req); err != nil { t.Fatal("inherited owner authority", err) }
	enginetest.RoleExecute(t, e, op, "RESET ROLE")
	enginetest.RoleExecute(t, e, op, "REVOKE postgres FROM shadow_reader")
	// Real catalog ACLs retain exact principal references. Deliberately retire
	// the fixture principal without SQL dependency cleanup to exercise stale
	// references, not DROP ROLE's separate dependency admission.
	state := cloneRoleFixture(e.RoleStore.Published)
	retireFixtureRole(state, reader.Ref)
	replacement := pgUserUnitRole(e, state, 30000, "shadow_reader")
	e.RoleStore.PublishLocked(state)
	stale := e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: e.Catalog.Current(), Catalog: e.Catalog.Current(), RoleRecord: state, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), State: authorization.RoleState{CurrentUser: reader.Ref}})
	if err := stale.Check(req); err == nil { t.Fatal("retired principal authorized") }
	stale.State.CurrentUser = replacement
	if err := stale.Check(access.Requirement{Object: column, Privileges: authorization.SelectPrivilege}); err == nil { t.Fatal("recreated principal inherited column ACL") }
	foreignPrincipal := engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	stale.State.CurrentUser = foreignPrincipal
	if err := stale.Check(req); err == nil { t.Fatal("foreign superuser authorized") }
}

func TestPGShadowStatementAndNestedSnapshot(t *testing.T) {
	e := engine.New(); d, ref := pgShadowUnitSource(t, e)
	old := cloneRoleFixture(e.RoleStore.Published); role := pgUserUnitRole(e, old, 20000, "snapshot"); e.RoleStore.PublishLocked(old)
	op, done := enginetest.RoleOperation(t, e, "postgres"); defer done()
	scope := &core.StatementScope{Op: op, Owner: op.Session.Transactions}
	e.Mu.Lock(); err := e.CaptureCommentSnapshotLocked(scope); e.Mu.Unlock(); if err != nil { t.Fatal(err) }
	open := func(scope *core.StatementScope, ref ids.ObjectRef) *exec.Source {
		t.Helper(); e.Mu.Lock()
		s, err := core.EngineOpenRelationSourceLocked(e, scope, ref, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
		if err != nil { t.Fatal(err) }; return s
	}
	first := open(scope, ref)
	updated := cloneRoleFixture(old); r := fixtureRole(updated, role); r.Password = "new verifier"; putFixtureRole(updated, r); e.RoleStore.PublishLocked(updated)
	second := open(scope, ref)
	addView := func(id ids.OID, name string, leaf ids.ObjectRef) ids.ObjectRef {
		v := *d; v.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}; v.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}; v.ObjectName = name; v.RelationKind = catalog.ViewRelation; v.VirtualView = false
		attrs := append([]int32(nil), d.Attributes...)
		v.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.SourceNode{QuerySchema: plan.QuerySchema(d.Columns), Ref: leaf}}, Sources: []stored.ViewLeaf{{Ref: leaf, Attributes: attrs, Reads: attrs}}})
		e.Catalog.State.SetDescriptor(v.Address(), &v); return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), v.Address())
	}
	inner := addView(20010, "shadow_inner", ref); outer := addView(20011, "shadow_outer", inner)
	nested := open(scope, outer); defer nested.Close()
	if nested.NestedInputs == nil || nested.NestedInputs.NestedInputs == nil { t.Fatal("nested capture missing") }
	for _, s := range []*exec.Source{first, second, nested.NestedInputs.NestedInputs} { if text, _ := pgShadowUnitRows(t, s)[20000][6].Text(); text != "unit-private-verifier" { t.Fatal("statement resampled credential") } }
	fresh := &core.StatementScope{Op: op, Owner: op.Session.Transactions}; e.Mu.Lock(); err = e.CaptureCommentSnapshotLocked(fresh); e.Mu.Unlock(); if err != nil { t.Fatal(err) }
	if text, _ := pgShadowUnitRows(t, open(fresh, ref))[20000][6].Text(); text != "new verifier" { t.Fatal("fresh credential stale") }
}

func TestPGShadowCancellation(t *testing.T) {
	e := engine.New(); d, ref := pgShadowUnitSource(t, e)
	state := emptyRoleFixture(e); pgUserUnitRole(e, state, 20000, "first"); pgUserUnitRole(e, state, 20001, "second")
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
			source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), state), Ref: ref, Source: d, Runtime: core.EngineCatalogRuntime{}}, registeredCatalogProducer(pgcatalog.ShadowOID))
			if source != nil || !errors.Is(err, context.Canceled) || ctx.calls != phase.at {
				t.Fatal("cancellation published credential prefix or missed check", phase.at, ctx.calls, err)
			}
		})
	}
	invalid := *d; invalid.Columns = append([]catalog.Column(nil), d.Columns...); invalid.Columns[1].Usage.Type = enginetest.Usage(e, sqltype.Int4).Type
	source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), state), Ref: ref, Source: &invalid, Runtime: core.EngineCatalogRuntime{}}, registeredCatalogProducer(pgcatalog.ShadowOID))
	if err == nil || source != nil { t.Fatal("invalid descriptor published rows") }
}
