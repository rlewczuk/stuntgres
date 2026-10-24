package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgRolesUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12007}
	d, ok := e.catalog.State.Relation(address)
	if !ok {
		t.Fatal("missing pg_roles")
	}
	return d, e.catalog.Domain.Ref(e.catalog.State.Epoch(), address)
}

func pgRolesUnitRows(t *testing.T, source *exec.Source) map[uint32][]sqltype.Value {
	t.Helper()
	if source == nil {
		t.Fatal("missing pg_roles capture")
	}
	bag := make(map[uint32][]sqltype.Value)
	for _, row := range source.Anonymous {
		if len(row) != 13 {
			t.Fatal("wrong pg_roles width")
		}
		id, err := row[12].OID()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := bag[id]; exists {
			t.Fatal("duplicate role identity")
		}
		mask, err := row[8].Text()
		if err != nil || mask != "********" {
			t.Fatal("unsafe password projection")
		}
		if !row[11].IsNull() || row[11].Type().ID() != sqltype.ID(1009) {
			t.Fatal("wrong typed NULL configuration")
		}
		bag[id] = row
	}
	return bag
}

func pgRolesUnitRole(e *Engine, state *access.RoleRecord, id uint64, name string) authorization.PrincipalRef {
	e.roles.InstallPrincipalFrontierLocked(id)
	ref, err := e.roles.PrincipalCandidateLocked()
	if err != nil {
		panic(err)
	}
	e.roles.InstallPrincipalFrontierLocked(ref.ID() + 1)
	putFixtureRole(state, authorization.RoleDescriptor{Ref: ref, Name: name, Login: true, Inherit: true, ConnectionLimit: -1, Password: "unit-private-verifier"})

	return ref
}

func TestPGRolesDescriptor(t *testing.T) {
	e := New()
	d, _ := pgRolesUnitSource(t, e)
	if d.Name() != "pg_roles" || !d.VirtualView || d.View != nil || d.RelationKind != catalog.CatalogRelation || d.Namespace().ObjectID != 11 || len(d.Columns) != 13 || len(d.Attributes) != 13 || len(d.Index) != 13 || d.ColumnMeta.HighWater != 13 {
		t.Fatal("descriptor shape")
	}
	names := []string{"rolname", "rolsuper", "rolinherit", "rolcreaterole", "rolcreatedb", "rolcanlogin", "rolreplication", "rolconnlimit", "rolpassword", "rolvaliduntil", "rolbypassrls", "rolconfig", "oid"}
	types := []sqltype.ID{sqltype.Name, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Int4, sqltype.Text, sqltype.Timestamptz, sqltype.Bool, 1009, sqltype.Oid}
	collations := []uint32{950, 0, 0, 0, 0, 0, 0, 0, 100, 0, 0, 950, 0}
	for i, c := range d.Columns {
		if c.Name != names[i] || c.Usage.Type.ID() != types[i] || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collations[i] || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 12007 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i {
			t.Fatal("exact pg_roles metadata", i, c)
		}
	}
	if !d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege || d.Security.ACL.Entries[0].Grantor != d.Owner {
		t.Fatal("PUBLIC SELECT")
	}
	if err := (relationProvider).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	if d.RowType != 12002 {
		t.Fatal("existing row identity not reused")
	}
	row := e.catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12002})
	array := e.catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12001})
	if row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Array != 12001 || row.SQLType.Concrete() {
		t.Fatal("row/array ownership links")
	}
	views := e.catalog.State.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12000})
	if views.Name() != "pg_views" || views.RowType != 12030 || d.Owner != e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres") || row.Owner != d.Owner || array.Owner != d.Owner {
		t.Fatal("adjacent identity or role ownership changed")
	}
	if pgcatalog.SummaryTable(12007) || pgcatalog.SummaryViewDefinition(12007) == "" {
		t.Fatal("pg_roles summary classification")
	}
}

func TestPGRolesRoleRoot(t *testing.T) {
	e := New()
	d, ref := pgRolesUnitSource(t, e)
	old := cloneRoleFixture(e.roles.Published)
	role := pgRolesUnitRole(e, old, 20000, "snapshot")
	e.roles.PublishLocked(old)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope := &StatementScope{op: op, owner: op.Session.Transactions}
	e.mu.Lock()
	err := e.captureCommentSnapshotLocked(scope)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	open := func(r ids.ObjectRef) *exec.Source {
		t.Helper()
		e.mu.Lock()
		source, err := e.openRelationSourceLocked(scope, r, e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return source
	}
	first := open(ref)
	updated := cloneRoleFixture(old)
	replacement := fixtureRole(updated, role)
	replacement.CreateDB = true
	putFixtureRole(updated, replacement)
	e.roles.PublishLocked(updated)
	second := open(ref)
	for _, source := range []*exec.Source{first, second} {
		flag, _ := pgRolesUnitRows(t, source)[20000][4].Boolean()
		if flag {
			t.Fatal("source opening resampled roles")
		}
	}
	addView := func(id ids.OID, name string, leaf ids.ObjectRef) ids.ObjectRef {
		view := *d
		view.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
		view.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		view.ObjectName = name
		view.RelationKind = catalog.ViewRelation
		view.VirtualView = false
		viewRef := e.catalog.Domain.Ref(e.catalog.State.Epoch(), view.Address())
		attrs := append([]int32(nil), d.Attributes...)
		view.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.SourceNode{QuerySchema: plan.QuerySchema(d.Columns), Ref: leaf}}, Sources: []stored.ViewLeaf{{Ref: leaf, Attributes: attrs, Reads: attrs}}})
		e.catalog.State.SetDescriptor(view.Address(), &view)
		return viewRef
	}
	inner := addView(20010, "pg_roles_inner", ref)
	outer := addView(20011, "pg_roles_outer", inner)
	nested := open(outer)
	defer nested.Close()
	if nested.NestedInputs == nil || nested.NestedInputs.NestedInputs == nil {
		t.Fatal("nested provider not opened")
	}
	flag, _ := pgRolesUnitRows(t, nested.NestedInputs.NestedInputs)[20000][4].Boolean()
	if flag {
		t.Fatal("nested provider resampled roles")
	}
	fresh := &StatementScope{op: op, owner: op.Session.Transactions}
	e.mu.Lock()
	err = e.captureCommentSnapshotLocked(fresh)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	current, err := e.openRelationSourceLocked(fresh, ref, e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	flag, _ = pgRolesUnitRows(t, current)[20000][4].Boolean()
	if !flag {
		t.Fatal("fresh statement retained stale flags")
	}
}

func TestPGRolesCancellation(t *testing.T) {
	e := New()
	d, ref := pgRolesUnitSource(t, e)
	state := cloneRoleFixture(e.roles.Published)
	pgRolesUnitRole(e, state, 20000, "first")
	pgRolesUnitRole(e, state, 20001, "second")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, roles := range []authorization.Read{e.roles.Read(&e.catalog.Owner, e.catalog.Current(), emptyRoleFixture(e)), e.roles.Read(&e.catalog.Owner, e.catalog.Current(), state)} {
		source, err := captureCatalogSourceRows(pgcatalog.Input{Context: canceled, Roles: roles, Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGRoles)
		if source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled admission published rows", err)
		}
	}
	for _, at := range []int{2, 3, state.Root.Read(authorization.PrincipalRef{}).RoleCount() + 2} {
		ctx := &pgShDescriptionCancelContext{Context: context.Background(), cancelAt: at}
		source, err := captureCatalogSourceRows(pgcatalog.Input{Context: ctx, Roles: e.roles.Read(&e.catalog.Owner, e.catalog.Current(), state), Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGRoles)
		if source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("iteration/final cancellation published partial rows", at, err)
		}
	}
	invalid := *d
	invalid.Columns = append([]catalog.Column(nil), d.Columns...)
	invalid.Columns[12].Usage.Type = testUsage(e, sqltype.Int4).Type
	if source, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.roles.Read(&e.catalog.Owner, e.catalog.Current(), state), Ref: ref, Source: &invalid, Runtime: engineCatalogRuntime{}}, capturePGRoles); err == nil || source != nil {
		t.Fatal("invalid OID construction published rows")
	}
	unknown := *d
	unknown.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20020}
	e.catalog.State.SetDescriptor(unknown.Address(), &unknown)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope := &StatementScope{op: op, owner: op.Session.Transactions}
	e.mu.Lock()
	source, err := e.openRelationSourceLocked(scope, e.catalog.Domain.Ref(e.catalog.State.Epoch(), unknown.Address()), e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
	if err == nil || source != nil {
		t.Fatal("unknown view routed to timezone")
	}
}

func TestPGRolesProjection(t *testing.T) {
	e := New()
	d, ref := pgRolesUnitSource(t, e)
	state := emptyRoleFixture(e)
	low := pgRolesUnitRole(e, state, 20000, "low")
	noLogin := pgRolesUnitRole(e, state, 20001, "nologin")
	pgRolesUnitRole(e, state, 4294967295, "max")
	pgRolesUnitRole(e, state, uint64(1)<<32|20000, "high")
	pgRolesUnitRole(e, state, 4294967296, "overflow")
	ordinary := fixtureRole(state, noLogin)
	ordinary.Login = false
	ordinary.ConnectionLimit = 0
	putFixtureRole(state, ordinary)
	setFixtureEdges(state, []authorization.Membership{{Role: low, Member: noLogin, Grantor: low, Admin: true, Inherit: true, Set: true}})
	for _, expiry := range []struct {
		has   bool
		stamp int64
	}{{false, 0}, {true, 123456789}, {true, sqltype.TimestampNegativeInfinity}, {true, sqltype.TimestampPositiveInfinity}} {
		for _, password := range []string{"", "plain-private", "md5-private", "SCRAM-SHA-256$private"} {
			role := fixtureRole(state, low)
			role.Superuser, role.CreateRole, role.CreateDB = true, true, true
			role.Inherit, role.Login, role.Replication, role.BypassRLS = false, true, true, true
			role.ConnectionLimit = 9
			role.HasValidUntil, role.ValidUntil, role.Password = expiry.has, expiry.stamp, password
			putFixtureRole(state, role)
			source, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.roles.Read(&e.catalog.Owner, e.catalog.Current(), state), Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGRoles)
			if err != nil {
				t.Fatal(err)
			}
			bag := pgRolesUnitRows(t, source)
			if len(bag) != 3 || bag[20000] == nil || bag[20001] == nil || bag[4294967295] == nil {
				t.Fatal("role visibility or uint32 identity boundary")
			}
			for id, row := range bag {
				for i, value := range row {
					if value.Type() != d.Columns[i].Usage.Type {
						t.Fatal("wrong typed column", i)
					}
				}
				name, err := row[0].Text()
				wantName := map[uint32]string{20000: "low", 20001: "nologin", 4294967295: "max"}[id]
				if err != nil || name != wantName {
					t.Fatal("role identity/name", id, name, err)
				}
				flags := []bool{false, true, false, false, id != 20001, false, false}
				if id == 20000 {
					flags = []bool{true, false, true, true, true, true, true}
				}
				for i, slot := range []int{1, 2, 3, 4, 5, 6, 10} {
					flag, err := row[slot].Boolean()
					if err != nil || flag != flags[i] {
						t.Fatal("direct stored flag", id, slot, err)
					}
				}
				limit, err := row[7].Integer()
				wantLimit := int64(-1)
				if id == 20000 {
					wantLimit = 9
				} else if id == 20001 {
					wantLimit = 0
				}
				if err != nil || limit != wantLimit {
					t.Fatal("stored connection limit", err)
				}
				if id == 20000 && expiry.has {
					stamp, err := row[9].Timestamp()
					if err != nil || stamp != expiry.stamp {
						t.Fatal("typed expiry", err)
					}
				} else if !row[9].IsNull() {
					t.Fatal("invented expiry")
				}
			}
			// Replacing role authority cannot mutate an already produced row.
			updated := cloneRoleFixture(state)
			changed := fixtureRole(updated, low)
			changed.Name, changed.Login, changed.ValidUntil = "replacement", false, 987
			putFixtureRole(updated, changed)
			e.roles.PublishLocked(updated)
			if name, _ := bag[20000][0].Text(); name != "low" {
				t.Fatal("retained role changed")
			}
			retireFixtureRole(updated, low)
			fresh, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.roles.Read(&e.catalog.Owner, e.catalog.Current(), updated), Ref: ref, Source: d, Runtime: engineCatalogRuntime{}}, capturePGRoles)
			if err != nil || pgRolesUnitRows(t, fresh)[20000] != nil {
				t.Fatal("high identity aliased removed role", err)
			}
		}
	}
}
