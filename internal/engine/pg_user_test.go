package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgUserUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12014}
	d, ok := e.Catalog.State.Relation(address)
	if !ok {
		t.Fatal("missing pg_user")
	}
	return d, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
}

func pgUserUnitRows(t *testing.T, source *exec.Source) map[uint32][]sqltype.Value {
	t.Helper()
	if source == nil {
		t.Fatal("missing pg_user capture")
	}
	bag := make(map[uint32][]sqltype.Value)
	for _, row := range source.Anonymous {
		if len(row) != 9 {
			t.Fatal("wrong pg_user width")
		}
		id, err := row[1].OID()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := bag[id]; exists {
			t.Fatal("duplicate role identity")
		}
		mask, err := row[6].Text()
		if err != nil || mask != "********" {
			t.Fatal("unsafe password projection")
		}
		if !row[8].IsNull() || row[8].Type().ID() != sqltype.ID(1009) {
			t.Fatal("wrong typed NULL configuration")
		}
		bag[id] = row
	}
	return bag
}

func pgUserUnitRole(e *Engine, state *access.RoleRecord, id uint64, name string) authorization.PrincipalRef {
	e.RoleStore.InstallPrincipalFrontierLocked(id)
	ref, err := e.RoleStore.PrincipalCandidateLocked()
	if err != nil {
		panic(err)
	}
	e.RoleStore.InstallPrincipalFrontierLocked(ref.ID() + 1)
	putFixtureRole(state, authorization.RoleDescriptor{Ref: ref, Name: name, Login: true, Password: "unit-private-verifier"})

	return ref
}

func TestPGUserDescriptor(t *testing.T) {
	e := New()
	d, _ := pgUserUnitSource(t, e)
	if d.Name() != "pg_user" || !d.VirtualView || d.View != nil || d.RelationKind != catalog.CatalogRelation || d.Namespace().ObjectID != 11 || len(d.Columns) != 9 || len(d.Attributes) != 9 || len(d.Index) != 9 || d.ColumnMeta.HighWater != 9 {
		t.Fatal("descriptor shape")
	}
	names := []string{"usename", "usesysid", "usecreatedb", "usesuper", "userepl", "usebypassrls", "passwd", "valuntil", "useconfig"}
	types := []sqltype.ID{sqltype.Name, sqltype.Oid, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Text, sqltype.Timestamptz, 1009}
	collations := []uint32{950, 0, 0, 0, 0, 0, 100, 0, 950}
	for i, c := range d.Columns {
		if c.Name != names[i] || c.Usage.Type.ID() != types[i] || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collations[i] || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 12014 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i {
			t.Fatal("exact pg_user metadata", i, c)
		}
	}
	if !d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege || d.Security.ACL.Entries[0].Grantor != d.Owner {
		t.Fatal("PUBLIC SELECT")
	}
	if err := (RelationProvider).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	if d.RowType != 12016 {
		t.Fatal("existing row identity not reused")
	}
	row := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12016})
	array := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12015})
	if row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Array != 12015 || row.SQLType.Concrete() {
		t.Fatal("row/array ownership links")
	}
	if pgcatalog.SummaryTable(12014) || pgcatalog.SummaryViewDefinition(12014) == "" {
		t.Fatal("pg_user summary classification")
	}
}

func TestPGUserTypedProjectionAndIdentityWidth(t *testing.T) {
	e := New()
	d, ref := pgUserUnitSource(t, e)
	state := emptyRoleFixture(e)
	low := pgUserUnitRole(e, state, 20000, "low")
	pgUserUnitRole(e, state, uint64(1)<<32|20000, "high")
	pgUserUnitRole(e, state, math.MaxUint32, "max")
	pgUserUnitRole(e, state, uint64(math.MaxUint32)+1, "overflow")
	hidden := pgUserUnitRole(e, state, 20001, "hidden")
	role := fixtureRole(state, hidden)
	role.Login = false
	putFixtureRole(state, role)
	role = fixtureRole(state, low)
	role.CreateDB = true
	role.Superuser = true
	role.Replication = true
	role.BypassRLS = true
	role.HasValidUntil = true
	role.ValidUntil = 123456789
	role.ConnectionLimit = 0
	putFixtureRole(state, role)
	source, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), state), Ref: ref, Source: d, Runtime: EngineCatalogRuntime{}}, capturePGUser)
	if err != nil {
		t.Fatal(err)
	}
	bag := pgUserUnitRows(t, source)
	if len(bag) != 2 || bag[20000] == nil || bag[math.MaxUint32] == nil {
		t.Fatal("OID-width or LOGIN filtering")
	}
	for id, row := range bag {
		for i, value := range row {
			if value.Type() != d.Columns[i].Usage.Type {
				t.Fatal("wrong concrete descriptor", i)
			}
		}
		name, _ := row[0].Text()
		if id == 20000 && name != "low" || id == math.MaxUint32 && name != "max" {
			t.Fatal("high identity aliased low")
		}
		for i := 2; i <= 5; i++ {
			flag, err := row[i].Boolean()
			if err != nil || flag != (id == 20000) {
				t.Fatal("direct flag projection", i, err)
			}
		}
		if id == 20000 {
			stamp, err := row[7].Timestamp()
			if err != nil || stamp != 123456789 {
				t.Fatal("typed expiry", err)
			}
		} else if !row[7].IsNull() {
			t.Fatal("invented expiry")
		}
	}
	// Publishing another root cannot mutate already-owned provider values.
	updated := cloneRoleFixture(state)
	changed := fixtureRole(updated, low)
	changed.Name = "replacement"
	changed.Login = false
	changed.ValidUntil = 987
	putFixtureRole(updated, changed)
	e.RoleStore.PublishLocked(updated)
	if name, _ := bag[20000][0].Text(); name != "low" {
		t.Fatal("detached name changed")
	}
	if stamp, _ := bag[20000][7].Timestamp(); stamp != 123456789 {
		t.Fatal("detached expiry changed")
	}
	retireFixtureRole(updated, low)
	fresh, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), updated), Ref: ref, Source: d, Runtime: EngineCatalogRuntime{}}, capturePGUser)
	if err != nil || pgUserUnitRows(t, fresh)[20000] != nil {
		t.Fatal("high identity resurrected removed low", err)
	}
}

func TestPGUserStatementAndNestedSnapshot(t *testing.T) {
	e := New()
	d, ref := pgUserUnitSource(t, e)
	old := cloneRoleFixture(e.RoleStore.Published)
	role := pgUserUnitRole(e, old, 20000, "snapshot")
	e.RoleStore.PublishLocked(old)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope := &StatementScope{Op: op, Owner: op.Session.Transactions}
	e.Mu.Lock()
	err := e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	open := func(r ids.ObjectRef) *exec.Source {
		t.Helper()
		e.Mu.Lock()
		source, err := EngineOpenRelationSourceLocked(e, scope, r, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
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
	e.RoleStore.PublishLocked(updated)
	second := open(ref)
	for _, source := range []*exec.Source{first, second} {
		flag, _ := pgUserUnitRows(t, source)[20000][2].Boolean()
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
		viewRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), view.Address())
		attrs := append([]int32(nil), d.Attributes...)
		view.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.SourceNode{QuerySchema: plan.QuerySchema(d.Columns), Ref: leaf}}, Sources: []stored.ViewLeaf{{Ref: leaf, Attributes: attrs, Reads: attrs}}})
		e.Catalog.State.SetDescriptor(view.Address(), &view)
		return viewRef
	}
	inner := addView(20010, "pg_user_inner", ref)
	outer := addView(20011, "pg_user_outer", inner)
	nested := open(outer)
	defer nested.Close()
	if nested.NestedInputs == nil || nested.NestedInputs.NestedInputs == nil {
		t.Fatal("nested provider not opened")
	}
	flag, _ := pgUserUnitRows(t, nested.NestedInputs.NestedInputs)[20000][2].Boolean()
	if flag {
		t.Fatal("nested provider resampled roles")
	}
	fresh := &StatementScope{Op: op, Owner: op.Session.Transactions}
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(fresh)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	current, err := EngineOpenRelationSourceLocked(e, fresh, ref, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	flag, _ = pgUserUnitRows(t, current)[20000][2].Boolean()
	if !flag {
		t.Fatal("fresh statement retained stale flags")
	}
}

func TestPGUserCancellationAndUnknownProvider(t *testing.T) {
	e := New()
	d, ref := pgUserUnitSource(t, e)
	state := cloneRoleFixture(e.RoleStore.Published)
	pgUserUnitRole(e, state, 20000, "first")
	pgUserUnitRole(e, state, 20001, "second")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, roles := range []authorization.Read{e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), emptyRoleFixture(e)), e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), state)} {
		source, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: canceled, Roles: roles, Ref: ref, Source: d, Runtime: EngineCatalogRuntime{}}, capturePGUser)
		if source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled admission published rows", err)
		}
	}
	for _, at := range []int{2, 3, 5} {
		ctx := &pgShDescriptionCancelContext{Context: context.Background(), cancelAt: at}
		source, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), state), Ref: ref, Source: d, Runtime: EngineCatalogRuntime{}}, capturePGUser)
		if source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("iteration/final cancellation published partial rows", at, err)
		}
	}
	invalid := *d
	invalid.Columns = append([]catalog.Column(nil), d.Columns...)
	invalid.Columns[1].Usage.Type = testUsage(e, sqltype.Int4).Type
	if source, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Roles: e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Current(), state), Ref: ref, Source: &invalid, Runtime: EngineCatalogRuntime{}}, capturePGUser); err == nil || source != nil {
		t.Fatal("invalid OID construction published rows")
	}
	unknown := *d
	unknown.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20020}
	e.Catalog.State.SetDescriptor(unknown.Address(), &unknown)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope := &StatementScope{Op: op, Owner: op.Session.Transactions}
	e.Mu.Lock()
	source, err := EngineOpenRelationSourceLocked(e, scope, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), unknown.Address()), e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
	if err == nil || source != nil {
		t.Fatal("unknown view routed to timezone")
	}
}

func TestPGUserResolvedWriteTargets(t *testing.T) {
	e := New()
	d, _ := pgUserUnitSource(t, e)
	for _, operation := range []string{"insert", "update", "delete"} {
		for _, password := range []bool{false, true} {
			targets := []int{2}
			if password {
				targets = []int{2, 6}
			}
			if operation == "delete" {
				targets = nil
			}
			err := pgcatalog.VirtualViewWriteError(d, operation, targets)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) {
				t.Fatal(err)
			}
			code := "55000"
			if password && operation != "delete" {
				code = "0A000"
			}
			if diagnostic.Code != code || diagnostic.Severity != "ERROR" || diagnostic.SeverityUnlocalized != "ERROR" || diagnostic.Position != "" || diagnostic.Context != "" || diagnostic.Column != "" {
				t.Fatal(diagnostic)
			}
		}
	}
	// Resolved attribute identity, not coincidental name spelling, chooses mask rejection.
	renamed := *d
	renamed.Columns = append([]catalog.Column(nil), d.Columns...)
	renamed.Columns[6].Name = "alias"
	if !reflect.DeepEqual(pgcatalog.VirtualViewWriteError(d, "update", []int{6}), pgcatalog.VirtualViewWriteError(&renamed, "update", []int{6})) {
		t.Fatal("write diagnostic depends on text spelling")
	}
}
