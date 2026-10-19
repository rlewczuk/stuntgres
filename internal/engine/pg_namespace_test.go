package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func namespaceUnitSource(t *testing.T, e *Engine, c catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2615}
	d, ok := c.Relation(a)
	if !ok {
		t.Fatal("missing namespace source")
	}
	return d, e.catalog.Domain.Ref(c.Epoch(), a)
}
func namespaceUnitCapture(t *testing.T, e *Engine, c catalog.Read) *querySource {
	t.Helper()
	d, ref := namespaceUnitSource(t, e, c)
	e.mu.Lock()
	rows, err := e.capturePGNamespace(context.Background(), c, e.roles.read(e.catalog.owner(), c, e.roles.published), ref, d)
	e.mu.Unlock()
	if err != nil || rows == nil || rows.ref != ref {
		t.Fatal(rows, err)
	}
	return rows
}
func namespaceUnitRow(t *testing.T, rows *querySource, id uint32) []sqltype.Value {
	t.Helper()
	var found []sqltype.Value
	for _, row := range rows.anonymous {
		oid, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		if oid == id {
			if found != nil {
				t.Fatal("duplicate namespace", id)
			}
			found = row
		}
	}
	if found == nil {
		t.Fatal("missing namespace", id)
	}
	return found
}

func TestPGNamespaceShape(t *testing.T) {
	e := New()
	c := e.CatalogRead()
	d, _ := namespaceUnitSource(t, e, c)
	if d.Name() != "pg_namespace" || d.Namespace().ObjectID != 11 || d.RelationKind != catalog.CatalogRelation || d.VirtualView || !d.History.HasIndexes || d.RowType != 10047 || len(d.Columns) != 4 || d.ColumnMeta.HighWater != 4 {
		t.Fatal("namespace relation shape", d)
	}
	names := [...]string{"oid", "nspname", "nspowner", "nspacl"}
	typeIDs := [...]sqltype.ID{26, 19, 26, 1034}
	for i, column := range d.Columns {
		typ, ok := c.LookupType(typeIDs[i])
		collation := uint32(0)
		if i == 1 {
			collation = 950
		}
		if !ok || column.Name != names[i] || column.Usage.Type != typ || column.Usage.Typmod != -1 || uint32(column.Usage.Collation.ID) != collation || column.Nullable != (i == 3) || !column.NullabilityKnown || column.OriginTable != 2615 || column.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[names[i]] != i {
			t.Fatal(i, column)
		}
	}
	for _, id := range []ids.OID{10047, 10046} {
		typ := c.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
		if typ.OwningRelation != d.Address() || typ.Owner != d.Owner {
			t.Fatal("row/array reciprocal owner", id, typ)
		}
	}
	if err := (relationProvider{}).Validate(c, d); err != nil {
		t.Fatal(err)
	}
	toast := c.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 99})
	if toast.Comment != "reserved schema for TOAST tables" || !pgDependPinned(c, toast.Address()) || ownershipRoot(c, toast) || !bootstrapNamespaceDumpable(toast) {
		t.Fatal("actual toast seed/protected root/control baseline", toast)
	}
}

func TestPGNamespaceCapture(t *testing.T) {
	e := New()
	c := e.CatalogRead()
	oid, revision := e.catalog.nextOID, e.catalog.nextRevision
	descriptors, names := maps.Collect(c.State().Descriptors()), maps.Collect(c.State().Names())
	old := namespaceUnitCapture(t, e, c)
	if old.count() != 4 {
		t.Fatal("pristine namespace inventory", old.count())
	}
	for id, name := range map[uint32]string{11: "pg_catalog", 99: "pg_toast", 2200: "public", 13229: "information_schema"} {
		row := namespaceUnitRow(t, old, id)
		got, err := row[1].Text()
		if err != nil || got != name {
			t.Fatal(name, got, err)
		}
		owner, err := row[2].OID()
		want := uint32(10)
		if id == 2200 {
			want = 6171
		}
		if err != nil || owner != want || row[3].IsNull() != (id == 99) {
			t.Fatal("builtin owner/ACL", id, owner, row[3], err)
		}
	}
	if e.catalog.state != c.State() || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || !maps.Equal(descriptors, maps.Collect(c.State().Descriptors())) || !maps.Equal(names, maps.Collect(c.State().Names())) {
		t.Fatal("capture mutated authority/frontiers")
	}
	stage := e.BeginCatalogStage(c)
	defer e.DiscardCatalogStage(stage)
	original := c.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200})
	next := *original
	next.ObjectName = "renamed_public"
	next.ObjectRevision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	private := namespaceUnitCapture(t, e, e.stageRead(stage))
	got, err := namespaceUnitRow(t, private, 2200)[1].Text()
	if err != nil || got != "renamed_public" {
		t.Fatal(got, err)
	}
	for _, snapshot := range []*querySource{old, namespaceUnitCapture(t, e, c)} {
		got, err := namespaceUnitRow(t, snapshot, 2200)[1].Text()
		if err != nil || got != "public" {
			t.Fatal("captured root changed", got, err)
		}
	}
	if e.catalog.state != c.State() || !maps.Equal(descriptors, maps.Collect(c.State().Descriptors())) || !maps.Equal(names, maps.Collect(c.State().Names())) {
		t.Fatal("private capture published")
	}
}

func TestPGNamespaceACL(t *testing.T) {
	e := New()
	c := e.CatalogRead()
	array, _ := c.LookupType(1034)
	roles := e.Roles()
	owner := roles.LookupRef("postgres")
	reader := roles.LookupRef("pg_read_all_data")
	for _, acl := range []authorization.ACLValue{{}, {Explicit: true}} {
		value, err := e.pgClassACL(roles, array, acl)
		if err != nil || value.IsNull() == acl.Explicit || acl.Explicit && value.ArrayLen() != 0 {
			t.Fatal("NULL versus empty", value, err)
		}
	}
	stage := e.BeginCatalogStage(c)
	defer e.DiscardCatalogStage(stage)
	empty := *c.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 99})
	empty.ACL = authorization.ACLValue{Explicit: true}
	empty.ObjectRevision++
	if err := stage.Replace(&empty); err != nil {
		t.Fatal(err)
	}
	stored := namespaceUnitRow(t, namespaceUnitCapture(t, e, e.stageRead(stage)), 99)[3]
	if stored.IsNull() || stored.ArrayLen() != 0 || !namespaceUnitRow(t, namespaceUnitCapture(t, e, c), 99)[3].IsNull() {
		t.Fatal("actual explicit-empty namespace ACL confused with captured NULL")
	}
	bits := []authorization.Privileges{0, authorization.UsagePrivilege, authorization.CreatePrivilege, authorization.UsagePrivilege | authorization.CreatePrivilege}
	masks := []uint32{0, 256, 512, 768}
	for i, privileges := range bits {
		for j, options := range bits {
			if options & ^privileges != 0 {
				continue
			}
			for _, public := range []bool{false, true} {
				acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader, Public: public}, Privileges: privileges, GrantOptions: options}}}
				if public {
					acl.Entries[0].Grantee.Role = authorization.PrincipalRef{}
				}
				value, err := e.pgClassACL(roles, array, acl)
				if err != nil || value.ArrayLen() != 1 {
					t.Fatal(value, err)
				}
				authority, grantee, grantor, mask, optionMask, err := value.ArrayElement(0).ACLItem()
				want := uint32(reader.ID())
				if public {
					want = 0
				}
				if err != nil || authority != &e.roles.aclAuthority || grantee != want || grantor != 10 || mask != masks[i] || optionMask != masks[j] {
					t.Fatal("schema ACL bits", grantee, grantor, mask, optionMask, err)
				}
			}
		}
	}
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.CreatePrivilege, GrantOptions: authorization.CreatePrivilege},
	}}
	merged := authorization.SchemaACLOwner(acl, owner, reader)
	value, err := e.pgClassACL(roles, array, merged)
	if err != nil || value.ArrayLen() != 1 {
		t.Fatal("owner merge", value, err)
	}
	_, grantee, grantor, mask, options, err := value.ArrayElement(0).ACLItem()
	if err != nil || grantee != uint32(reader.ID()) || grantor != grantee || mask != 768 || options != 512 {
		t.Fatal(grantee, grantor, mask, options, err)
	}
	foreign := New()
	for _, bad := range []authorization.PrincipalRef{foreign.roles.domain.Principal(10), e.roles.domain.Principal(999999)} {
		for _, grantor := range []bool{false, true} {
			broken := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.UsagePrivilege}}}
			if grantor {
				broken.Entries[0].Grantor = bad
			} else {
				broken.Entries[0].Grantee.Role = bad
			}
			if _, err := e.pgClassACL(roles, array, broken); err == nil {
				t.Fatal("foreign/stale ACL principal admitted")
			}
		}
	}
}

func TestPGNamespaceAuthority(t *testing.T) {
	e := New()
	c := e.CatalogRead()
	d, ref := namespaceUnitSource(t, e, c)
	foreign := New()
	reject := func(catalog catalog.Read, roles authorization.Read, r ids.ObjectRef, source *catalog.Relation) {
		t.Helper()
		e.mu.Lock()
		rows, err := e.capturePGNamespace(context.Background(), catalog, roles, r, source)
		e.mu.Unlock()
		if err == nil || rows != nil {
			t.Fatal("invalid capture authority admitted", rows, err)
		}
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			t.Fatal("invariant fabricated SQLSTATE", diagnostic)
		}
	}
	reject(foreign.CatalogRead(), e.Roles(), ref, d)
	reject(c, foreign.Roles(), ref, d)
	forged := ref
	forged = foreign.catalog.Domain.Ref(forged.Epoch, forged.Address)
	reject(c, e.Roles(), forged, d)
	forged = ref
	forged.Epoch++
	reject(c, e.Roles(), forged, d)
	forged = ref
	forged.Address.SubID = 1
	reject(c, e.Roles(), forged, d)
	reject(c, e.Roles(), ref, nil)
	for _, width := range []int{0, 3, 5} {
		next := catalog.CloneState(c.State())
		invalid := *d
		invalid.Columns = make([]catalog.Column, width)
		next.SetDescriptor(d.Address(), &invalid)
		reject(e.catalog.read(next), e.Roles(), ref, &invalid)
	}
	for i := range d.Columns {
		next := catalog.CloneState(c.State())
		invalid := *d
		invalid.Columns = append([]catalog.Column(nil), d.Columns...)
		invalid.Columns[i].Usage.Type, _ = foreign.CatalogRead().LookupType(d.Columns[i].Usage.Type.ID())
		next.SetDescriptor(d.Address(), &invalid)
		reject(e.catalog.read(next), e.Roles(), ref, &invalid)
	}
	ns := c.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 99})
	for _, owner := range []authorization.PrincipalRef{foreign.roles.domain.Principal(10), e.roles.domain.Principal(999999), e.roles.domain.Principal(1 << 32)} {
		next := catalog.CloneState(c.State())
		invalid := *ns
		invalid.Owner = owner
		next.SetDescriptor(ns.Address(), &invalid)
		reject(e.catalog.read(next), e.Roles(), ref, d)
	}
	next := catalog.CloneState(c.State())
	invalid := *ns
	invalid.ObjectName = "stale_toast"
	next.SetDescriptor(ns.Address(), &invalid)
	reject(e.catalog.read(next), e.Roles(), ref, d)
	invalid = *ns
	invalid.TemporaryToast = true
	next.SetDescriptor(ns.Address(), &invalid)
	reject(e.catalog.read(next), e.Roles(), ref, d)
	next = catalog.CloneState(c.State())
	next.SetDescriptor(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}, ns)
	reject(e.catalog.read(next), e.Roles(), ref, d)
	next = catalog.CloneState(c.State())
	invalid = *ns
	invalid.Temporary = foreign.catalog.Domain.Session(1)
	next.SetDescriptor(ns.Address(), &invalid)
	reject(e.catalog.read(next), e.Roles(), ref, d)
	next = catalog.CloneState(c.State())
	next.SetDescriptor(catalog.CurrentDatabaseAddress, (*catalog.Database)(nil))
	reject(e.catalog.read(next), e.Roles(), ref, d)
	database := *c.MustDatabase(catalog.CurrentDatabaseAddress)
	database.Owner = e.Roles().LookupRef("pg_read_all_data")
	next = catalog.CloneState(c.State())
	next.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
	reject(e.catalog.read(next), e.Roles(), ref, d)
}

type namespaceCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	calls, at int
}

func (c *namespaceCancelContext) Err() error {
	c.calls++
	if c.calls == c.at {
		c.cancel()
	}
	return c.Context.Err()
}
func TestPGNamespaceCancellation(t *testing.T) {
	e := New()
	c := e.CatalogRead()
	d, ref := namespaceUnitSource(t, e, c)
	oid, revision := e.catalog.nextOID, e.catalog.nextRevision
	roles := e.Roles()
	for _, at := range []int{1, 3, 6} {
		ctx, cancel := context.WithCancel(context.Background())
		controlled := &namespaceCancelContext{Context: ctx, cancel: cancel, at: at}
		e.mu.Lock()
		rows, err := e.capturePGNamespace(controlled, c, roles, ref, d)
		e.mu.Unlock()
		cancel()
		if !errors.Is(err, context.Canceled) || rows != nil || e.catalog.state != c.State() || e.catalog.nextOID != oid || e.catalog.nextRevision != revision {
			t.Fatal("cancellation published partial capture", at, rows, err)
		}
	}
	if namespaceUnitCapture(t, e, c).count() != 4 {
		t.Fatal("capture unusable after cancellation")
	}
}

func TestPGNamespaceFixtureOwnership(t *testing.T) {
	e := New()
	run(t, e, "CREATE USER namespace_fixture_actor")
	actor := e.Roles().LookupRef("namespace_fixture_actor")
	op, done := roleOperation(t, e, "namespace_fixture_actor")
	defer done()
	before := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	original := before.MustNamespace(address)
	identity := op.RoleState()
	withPublicSchemaUnitOwner(t, e, "namespace_fixture_actor", func() {
		current := e.CatalogRead().MustNamespace(address)
		if current.Owner != actor || current.Address() != original.Address() || op.RoleState() != identity {
			t.Fatal("fixture bypassed real namespace/caller authority")
		}
		roleExecute(t, e, op, "CREATE TABLE namespace_fixture_table(i INT)")
		roleExecute(t, e, op, "INSERT INTO namespace_fixture_table VALUES(18)")
	})
	check := func() {
		t.Helper()
		current := e.CatalogRead().MustNamespace(address)
		if current.Owner != original.Owner || current.Address() != original.Address() || current.ACL.Explicit != original.ACL.Explicit || !slices.Equal(current.ACL.Entries, original.ACL.Entries) || op.RoleState() != identity {
			t.Fatal("fixture did not restore exact owner/ACL/caller authority")
		}
	}
	check()
	table := e.CatalogRead().MustRelation(roleTableRef(t, e, "namespace_fixture_table").Address)
	if table.Owner != actor || table.Namespace() != address {
		t.Fatal("fixture changed real object creator", table)
	}
	roleErrorPosition(t, e, op, "CREATE TABLE namespace_fixture_denied(i INT)", "42501", "permission denied for schema public", "", "14")
	marker := new(int)
	func() {
		defer func() {
			if got := recover(); got != marker {
				t.Fatalf("fixture panic propagation: %v", got)
			}
		}()
		withPublicSchemaUnitOwner(t, e, "namespace_fixture_actor", func() { panic(marker) })
	}()
	check()
	if before.Get(address) != original || original.Owner.ID() != 6171 {
		t.Fatal("fixture changed immutable pristine snapshot")
	}
}
