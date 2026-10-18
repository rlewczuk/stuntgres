package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgExtensionUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3079}
	d, ok := e.catalog.state.Relation(a)
	if !ok {
		t.Fatal("missing pg_extension")
	}
	return d, e.catalog.domain.Ref(e.catalog.state.epoch, a)
}

func pgExtensionUnitShape(t *testing.T, e *Engine) *relationDescriptor {
	t.Helper()
	d, _ := pgExtensionUnitSource(t, e)
	owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	if d.name != "pg_extension" || d.namespace.ObjectID != 11 || d.relationKind != catalogRelation || d.attributeHighWater != 8 || len(d.columns) != 8 || d.owner != owner || owner.ID() != 10 {
		t.Fatal("relation identity", d, owner)
	}
	names := [8]string{"oid", "extname", "extowner", "extnamespace", "extrelocatable", "extversion", "extconfig", "extcondition"}
	typeIDs := [8]sqltype.ID{26, 19, 26, 26, 16, 25, 1028, 1009}
	collations := [8]uint32{0, 950, 0, 0, 0, 950, 0, 950}
	for i, c := range d.columns {
		owned, ok := e.CatalogRead().LookupType(typeIDs[i])
		if !ok || c.Name != names[i] || c.Usage.Type != owned || c.Usage.Type.ID() != typeIDs[i] || c.Usage.Typmod != -1 || uint32(c.Usage.Collation.ID) != collations[i] || c.Nullable != (i >= 6) || !c.NullabilityKnown || c.OriginTable != 3079 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[names[i]] != i {
			t.Fatal("column", i, c)
		}
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	if len(d.acl.Entries) != 1 || !d.acl.Explicit || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Grantor != owner || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.acl)
	}
	if d.rowType != 10074 {
		t.Fatal("existing native rowtype", d.rowType)
	}
	row := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10074})
	array := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10073})
	ownedRow, ok := e.CatalogRead().LookupType(10074)
	ownedArray, arrayOK := e.CatalogRead().LookupArray(ownedRow)
	if !ok || !arrayOK || ownedRow != row.typ || ownedArray != array.typ || row.array != 10073 || array.typ.Element() != row.typ || row.owningRelation != d.address || array.owningRelation != d.address || row.owner != owner || array.owner != owner {
		t.Fatal("row/array ownership", row, array)
	}
	return d
}

func TestPGExtensionShapeAndOwnership(t *testing.T) {
	e := New()
	d := pgExtensionUnitShape(t, e)
	foreign, _ := New().CatalogRead().LookupType(sqltype.Oid)
	invalid := *d
	invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
	invalid.columns[0].Usage.Type = foreign
	if err := (relationProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
		t.Fatal("foreign generation accepted")
	}
	class := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	rows, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roleRead(e.catalog.read(e.catalog.state), e.roles), ids.ObjectRef{}, class)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows.anonymous {
		oid, _ := row[0].OID()
		if oid != 3079 {
			continue
		}
		name, _ := row[1].Text()
		ns, _ := row[2].OID()
		kind, _ := row[class.index["relkind"]].Text()
		count, _ := row[class.index["relnatts"]].Integer()
		if name != "pg_extension" || ns != 11 || kind != "r" || count != 8 {
			t.Fatal("pg_class projection", row)
		}
		found++
	}
	if found != 1 {
		t.Fatal("pg_class identity count", found)
	}
}

func TestPGExtensionCapture(t *testing.T) {
	e := New()
	d, ref := pgExtensionUnitSource(t, e)
	before, names := maps.Clone(e.catalog.state.descriptors), maps.Clone(e.catalog.state.names)
	old, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roleRead(e.catalog.read(e.catalog.state), e.roles), ref, d)
	if err != nil || old == nil {
		t.Fatal("capture", old, err)
	}
	if old.ref != ref || old.anonymous == nil || old.count() != 0 || !maps.Equal(before, e.catalog.state.descriptors) || !maps.Equal(names, e.catalog.state.names) {
		t.Fatal("empty immutable capture", old)
	}
	root := &projectNode{querySchema: querySchema(d.columns), child: &sourceNode{querySchema: querySchema(d.columns), ref: ref}}
	for i, c := range d.columns {
		root.expressions = append(root.expressions, columnExpression{expressionInfo: expressionInfo{Usage: c.Usage}, origin: ref, scope: 1, frame: 1, slot: i})
	}
	executable, err := captureExtensionCatalog(context.Background(), e.CatalogRead(), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	rows, count, err := finalizeQuery(context.Background(), root, executable, nil)
	if err != nil {
		t.Fatal("empty captured source must execute", err)
	}
	if count != 0 || !reflect.DeepEqual(rows.Schema(), d.columns) {
		t.Fatal("empty result shape/count", rows.Schema(), count)
	}
	var emptyDestination [8]sqltype.Value
	if next, err := rows.Next(context.Background(), emptyDestination[:]); next || err != nil {
		t.Fatal("empty result row", next, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{0, 7, 9} {
		invalid := *d
		invalid.columns = make([]ColumnDescriptor, width)
		if rows, err := captureExtensionCatalog(context.Background(), e.CatalogRead(), ref, &invalid); err == nil || rows != nil {
			t.Fatal("malformed width", width, rows, err)
		}
	}
	foreign := New()
	for i, c := range d.columns {
		invalid := *d
		invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
		invalid.columns[i].Usage.Type, _ = foreign.CatalogRead().LookupType(c.Usage.Type.ID())
		if rows, err := captureExtensionCatalog(context.Background(), e.CatalogRead(), ref, &invalid); err == nil || rows != nil {
			t.Fatal("foreign column", i, rows, err)
		}
	}
	state := *e.catalog.state
	state.descriptors = maps.Clone(e.catalog.state.descriptors)
	for a := range state.Descriptors() {
		if a.ClassID == ids.RelationClassID {
			state.removeDescriptor(a)
		}
	}
	view := e.catalog.read(&state)
	if rows, err := e.captureCatalogSourceLocked(context.Background(), view, e.roleRead(view, e.roles), ref, d); err != nil || rows == nil || rows.count() != 0 {
		t.Fatal("unrelated inventory dependency", rows, err)
	}
	state.removeDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 1028})
	if rows, err := captureExtensionCatalog(context.Background(), view, ref, d); err == nil || rows != nil {
		t.Fatal("missing array descriptor", rows, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rows, err := captureExtensionCatalog(ctx, e.CatalogRead(), ref, d); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", rows, err)
	}
	retained := &indexedStage{source: old, schema: d.columns}
	original := append([]ColumnDescriptor(nil), retained.Schema()...)
	for _, replace := range []func() error{
		func() error { return e.Reset(context.Background()) },
		func() error { _, err := replaceWithFreshCatalog(context.Background(), e); return err },
	} {
		if err := replace(); err != nil {
			t.Fatal(err)
		}
		fresh := pgExtensionUnitShape(t, e)
		for i, c := range fresh.columns {
			if c.Usage.Type == d.columns[i].Usage.Type {
				t.Fatal("generation not reminted", i)
			}
		}
		if rows, err := captureExtensionCatalog(context.Background(), e.CatalogRead(), ref, d); err == nil || rows != nil {
			t.Fatal("old source accepted in replacement", rows, err)
		}
		if !reflect.DeepEqual(retained.Schema(), original) || old.ref != ref || old.count() != 0 {
			t.Fatal("retained source/schema changed")
		}
	}
	var destination [8]sqltype.Value
	if next, err := retained.Next(context.Background(), destination[:]); next || err != nil {
		t.Fatal("empty retained EOF", next, err)
	}
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retained.Schema(), original) {
		t.Fatal("schema lost after EOF/Close")
	}
}

func TestPGExtensionCaptureInstalled(t *testing.T) {
	e := New()
	d, ref := pgExtensionUnitSource(t, e)
	empty, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roleRead(e.catalog.read(e.catalog.state), e.roles), ref, d)
	if err != nil || empty == nil || empty.count() != 0 {
		t.Fatal("fresh installed catalog", empty, err)
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	stage := extensionUnitStage(t, e, op)
	extensionUnitPublish(t, e, op, stage)
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
	ext := e.catalog.state.MustExtension(address)
	d = pgExtensionUnitShape(t, e)
	captured, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roleRead(e.catalog.read(e.catalog.state), e.roles), ref, d)
	if err != nil || captured == nil || captured.count() != 1 {
		t.Fatal("installed descriptor not projected", captured, err)
	}
	row := captured.anonymous[0]
	for i, expected := range []ids.OID{ext.address.ObjectID, ids.OID(ext.owner.ID()), ext.targetNamespace.ObjectID} {
		actual, err := row[[]int{0, 2, 3}[i]].OID()
		if err != nil || actual != uint32(expected) {
			t.Fatal("descriptor-derived identity", i, actual, expected, err)
		}
	}
	name, nameErr := row[1].Text()
	version, versionErr := row[5].Text()
	relocatable, relocatableErr := row[4].Boolean()
	if nameErr != nil || versionErr != nil || relocatableErr != nil || name != ext.name || version != ext.version || !relocatable || !row[6].IsNull() || !row[7].IsNull() {
		t.Fatal("installed descriptor attributes", row, nameErr, versionErr, relocatableErr)
	}
	if empty.count() != 0 || captured.ref != ref {
		t.Fatal("capture resampled or lost bound identity", empty, captured)
	}
}
