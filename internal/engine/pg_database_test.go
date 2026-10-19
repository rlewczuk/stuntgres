package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func pgDatabaseUnitSource(t *testing.T, e *Engine, catalog catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1262}
	source, ok := catalog.Relation(address)
	if !ok {
		t.Fatal("missing pg_database")
	}
	return source, e.catalog.Domain.Ref(catalog.Epoch(), address)
}

func pgDatabaseUnitCapture(t *testing.T, e *Engine, catalog catalog.Read) *querySource {
	t.Helper()
	source, ref := pgDatabaseUnitSource(t, e, catalog)
	e.mu.Lock()
	rows, err := e.captureCatalogSourceLocked(context.Background(), catalog, e.roleRead(catalog, e.roles), ref, source)
	e.mu.Unlock()
	if err != nil || rows == nil || rows.ref != ref || rows.count() != 1 || len(rows.anonymous[0]) != 18 {
		t.Fatal("one captured database", rows, err)
	}
	return rows
}

func TestPGDatabaseShapeAndOwnership(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, _ := pgDatabaseUnitSource(t, e, view)
	owner := e.Roles().LookupRef("postgres")
	if d.Name() != "pg_database" || d.Namespace().ObjectID != 11 || d.RelationKind != catalog.CatalogRelation || d.VirtualView || d.AttributeHighWater != 18 || len(d.Columns) != 18 || d.Owner != owner || !d.History.HasIndexes {
		t.Fatal("relation identity", d)
	}
	names := [...]string{"oid", "datname", "datdba", "encoding", "datlocprovider", "datistemplate", "datallowconn", "dathasloginevt", "datconnlimit", "datfrozenxid", "datminmxid", "dattablespace", "datcollate", "datctype", "datlocale", "daticurules", "datcollversion", "datacl"}
	typeIDs := [...]sqltype.ID{26, 19, 26, 23, 18, 16, 16, 16, 23, 28, 28, 26, 25, 25, 25, 25, 25, 1034}
	collations := [...]uint32{0, 950, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 950, 950, 950, 950, 950, 0}
	nulls := [...]bool{false, false, false, false, false, true, true, false, true, true, true, true, false, false, true, true, true, true}
	for i, column := range d.Columns {
		typ, ok := view.LookupType(typeIDs[i])
		if !ok || column.Name != names[i] || column.Usage.Type != typ || column.Usage.Typmod != -1 || uint32(column.Usage.Collation.ID) != collations[i] || column.Nullable != nulls[i] || !column.NullabilityKnown || column.OriginTable != 1262 || column.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[names[i]] != i {
			t.Fatal("column", i, column)
		}
	}
	if err := (relationProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if !d.ACL.Explicit || len(d.ACL.Entries) != 1 || !d.ACL.Entries[0].Grantee.Public || d.ACL.Entries[0].Grantor != owner || d.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.ACL)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 1248})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10052})
	ownedRow, ok := view.LookupType(1248)
	ownedArray, arrayOK := view.LookupArray(ownedRow)
	if d.RowType != 1248 || !ok || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || row.Array != 10052 || array.SQLType.Element() != row.SQLType || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Owner != owner || array.Owner != owner {
		t.Fatal("row/array ownership", row, array)
	}
}

func TestPGDatabaseCapture(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, ref := pgDatabaseUnitSource(t, e, view)
	state, oid, revision := e.catalog.state, e.catalog.nextOID, e.catalog.nextRevision
	descriptors, names := maps.Collect(state.Descriptors()), maps.Collect(state.Names())
	rows := pgDatabaseUnitCapture(t, e, view)
	row := rows.anonymous[0]
	for i, value := range row {
		wantNull := i == 5 || i == 6 || i >= 8 && i <= 11 || i >= 14 && i <= 17
		if value.Type() != d.Columns[i].Usage.Type || value.IsNull() != wantNull {
			t.Fatal("concrete carrier/knownness", i, value)
		}
	}
	for i, want := range map[int]uint32{0: 16383, 2: 10} {
		if got, err := row[i].OID(); err != nil || got != want {
			t.Fatal("OID", i, got, want, err)
		}
	}
	database := view.MustDatabase(catalog.CurrentDatabaseAddress)
	for i, want := range map[int]string{1: database.Name(), 4: "c", 12: "C", 13: "C"} {
		if got, err := row[i].Text(); err != nil || got != want {
			t.Fatal("text", i, got, want, err)
		}
	}
	if got, err := row[3].Integer(); err != nil || got != 6 {
		t.Fatal("UTF8", got, err)
	}
	if got, err := row[7].Boolean(); err != nil || got {
		t.Fatal("login events", got, err)
	}
	if !row[17].IsNull() || database.ACL.Explicit || len(database.ACL.Entries) != 0 {
		t.Fatal("absent database ACL baseline", row[17], database.ACL)
	}
	unchanged := func() {
		if e.catalog.state != state || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || !maps.Equal(descriptors, maps.Collect(state.Descriptors())) || !maps.Equal(names, maps.Collect(state.Names())) {
			t.Fatal("capture mutated catalog/frontier")
		}
	}
	unchanged()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := e.capturePGDatabase(ctx, view, e.Roles(), ref, d); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", got, err)
	}
	unchanged()
	foreign := New()
	for _, tc := range []struct {
		catalog catalog.Read
		roles   authorization.Read
		ref     ids.ObjectRef
	}{{foreign.CatalogRead(), e.Roles(), ref}, {view, foreign.Roles(), ref}, {view, e.Roles(), foreign.catalog.Domain.Ref(ref.Epoch, ref.Address)}} {
		if got, err := e.capturePGDatabase(context.Background(), tc.catalog, tc.roles, tc.ref, d); got != nil || err == nil {
			t.Fatal("foreign capture admitted", got, err)
		}
	}
	for i, column := range d.Columns {
		invalid := *d
		invalid.Columns = append([]catalog.Column(nil), d.Columns...)
		invalid.Columns[i].Usage.Type, _ = foreign.CatalogRead().LookupType(column.Usage.Type.ID())
		if got, err := e.capturePGDatabase(context.Background(), view, e.Roles(), ref, &invalid); got != nil || err == nil {
			t.Fatal("foreign concrete type", i, got, err)
		}
	}
	for _, width := range []int{0, 17, 19} {
		invalid := *d
		invalid.Columns = make([]catalog.Column, width)
		if got, err := e.capturePGDatabase(context.Background(), view, e.Roles(), ref, &invalid); got != nil || err == nil {
			t.Fatal("invalid source width", width, got, err)
		}
	}
	private := catalog.CloneState(view.State())
	for address := range private.Descriptors() {
		if address.ClassID == ids.RelationClassID {
			private.RemoveDescriptor(address)
		}
	}
	isolated := e.catalog.read(private)
	if got, err := e.capturePGDatabase(context.Background(), isolated, e.roleRead(isolated, e.roles), ref, d); got == nil || err != nil || got.count() != 1 {
		t.Fatal("unrelated relations affect current row", got, err)
	}
	invalidDB := *database
	invalidDB.Owner = foreign.authorizationDomain.Principal(10)
	private.SetDescriptor(catalog.CurrentDatabaseAddress, &invalidDB)
	if got, err := e.capturePGDatabase(context.Background(), isolated, e.roleRead(isolated, e.roles), ref, d); got != nil || err == nil {
		t.Fatal("foreign owner admitted", got, err)
	}
	invalidDB.Owner = e.authorizationDomain.Principal(999999)
	if got, err := e.capturePGDatabase(context.Background(), isolated, e.roleRead(isolated, e.roles), ref, d); got != nil || err == nil {
		t.Fatal("stale owner admitted", got, err)
	}
	unchanged()
}

func TestPGDatabasePublication(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	old := pgDatabaseUnitCapture(t, e, base)
	d, ref := pgDatabaseUnitSource(t, e, base)
	original := base.MustDatabase(catalog.CurrentDatabaseAddress)
	successor, ok := e.Roles().Lookup("pg_read_all_data")
	if !ok {
		t.Fatal("missing predefined principal")
	}
	next := *original
	next.Owner, next.ObjectRevision = successor.Ref, original.Revision()+1
	next.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: successor.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.CreatePrivilege}}}
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	private := e.stageRead(stage)
	own := pgDatabaseUnitCapture(t, e, private)
	peer := pgDatabaseUnitCapture(t, e, base)
	check := func(rows *querySource, owner uint32, public bool) {
		t.Helper()
		row := rows.anonymous[0]
		if id, err := row[2].OID(); err != nil || id != owner {
			t.Fatal("captured owner", id, owner, err)
		}
		if !public {
			if !row[17].IsNull() {
				t.Fatal("retained baseline ACL is not NULL", row[17])
			}
			return
		}
		_, grantee, grantor, mask, options, err := row[17].ArrayElement(0).ACLItem()
		if err != nil || grantee != 0 || grantor != owner || mask != 512 || options != 0 {
			t.Fatal("captured ACL", grantee, grantor, mask, options, err)
		}
	}
	check(own, uint32(successor.Ref.ID()), true)
	check(peer, 10, false)
	check(old, 10, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) || e.CatalogRead().State() != base.State() {
		t.Fatal("canceled publication changed base", err)
	}
	publication, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(publication, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	check(pgDatabaseUnitCapture(t, e, e.CatalogRead()), uint32(successor.Ref.ID()), true)
	check(old, 10, false)
	check(peer, 10, false)
	retained := &indexedStage{source: old, schema: d.Columns}
	schema := append([]catalog.Column(nil), retained.Schema()...)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	check(pgDatabaseUnitCapture(t, e, e.CatalogRead()), uint32(successor.Ref.ID()), true)
	fresh, _ := pgDatabaseUnitSource(t, e, e.CatalogRead())
	if fresh.Columns[0].Usage.Type == d.Columns[0].Usage.Type {
		t.Fatal("replacement did not remint descriptors")
	}
	if got, err := e.capturePGDatabase(context.Background(), e.CatalogRead(), e.Roles(), ref, d); got != nil || err == nil {
		t.Fatal("stale source admitted after replacement", got, err)
	}
	values := make([]sqltype.Value, 18)
	if next, err := retained.Next(context.Background(), values); !next || err != nil {
		t.Fatal("retained row", next, err)
	}
	if !reflect.DeepEqual(values, old.anonymous[0]) {
		t.Fatal("retained values changed")
	}
	if next, err := retained.Next(context.Background(), values); next || err != nil {
		t.Fatal("retained EOF", next, err)
	}
	if err := retained.Close(); err != nil || !reflect.DeepEqual(schema, retained.Schema()) {
		t.Fatal("retained schema after Close", err)
	}
}
