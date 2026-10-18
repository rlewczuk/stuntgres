package engine

import (
	"context"
	"errors"
	"maps"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func pgCollationUnitSource(t *testing.T, e *Engine, catalog CatalogRead) (*relationDescriptor, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: RelationClassID, ObjectID: 3456}
	d, ok := catalog.state.descriptors[address].(*relationDescriptor)
	if !ok {
		t.Fatal("missing pg_collation")
	}
	return d, e.domain.Ref(catalog.Epoch(), address)
}

func TestPGCollationShapeAndOwnership(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	d, _ := pgCollationUnitSource(t, e, catalog)
	owner := e.Roles().LookupRef("postgres")
	if d.name != "pg_collation" || d.namespace.ObjectID != 11 || d.owner != owner || d.relationKind != catalogRelation || d.virtualView || d.attributeHighWater != 12 || len(d.columns) != 12 {
		t.Fatal("catalog identity", d)
	}
	names := [...]string{"oid", "collname", "collnamespace", "collowner", "collprovider", "collisdeterministic", "collencoding", "collcollate", "collctype", "colllocale", "collicurules", "collversion"}
	typeIDs := [...]sqltype.ID{26, 19, 26, 26, 18, 16, 23, 25, 25, 25, 25, 25}
	collations := [...]uint32{0, 950, 0, 0, 0, 0, 0, 950, 950, 950, 950, 950}
	for i, column := range d.columns {
		typ, ok := catalog.LookupType(typeIDs[i])
		if !ok || column.Name != names[i] || column.Usage.Type != typ || column.Usage.Typmod != -1 || uint32(column.Usage.Collation.ID) != collations[i] || column.Nullable != (i >= 7) || !column.NullabilityKnown || column.OriginTable != 3456 || column.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[names[i]] != i {
			t.Error("release attribute", i+1, column)
		}
	}
	if err := (relationProvider{}).Validate(catalog, d); err != nil {
		t.Fatal(err)
	}
	if !d.acl.Explicit || len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Grantor != owner || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.acl)
	}
	row, ok := catalog.state.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(d.rowType)}].(*typeDescriptor)
	if !ok {
		t.Fatal("owned row type", d.rowType)
	}
	array, ok := catalog.state.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(row.array)}].(*typeDescriptor)
	if !ok {
		t.Fatal("owned row array", row.array)
	}
	ownedRow, rowOK := catalog.LookupType(d.rowType)
	ownedArray, arrayOK := catalog.LookupArray(ownedRow)
	if !rowOK || !arrayOK || row.typ != ownedRow || array.typ != ownedArray || array.typ.Element() != row.typ || row.owningRelation != d.address || array.owningRelation != d.address || row.owner != owner || array.owner != owner {
		t.Fatal("relational row/array ownership", row, array)
	}
}

func pgCollationUnitCapture(t *testing.T, e *Engine, catalog CatalogRead) *querySource {
	t.Helper()
	d, ref := pgCollationUnitSource(t, e, catalog)
	state, oid, revision := e.state, e.nextOID, e.nextRevision
	descriptors, names := maps.Clone(state.descriptors), maps.Clone(state.names)
	capturedDescriptors, capturedNames := maps.Clone(catalog.state.descriptors), maps.Clone(catalog.state.names)
	e.mu.Lock()
	rows, err := e.captureCatalogSourceLocked(context.Background(), catalog, e.roleRead(catalog, e.roles), ref, d)
	e.mu.Unlock()
	if err != nil || rows == nil || rows.ref != ref {
		t.Fatal("capture", rows, err)
	}
	count := 0
	for _, descriptor := range catalog.state.descriptors {
		if _, ok := descriptor.(*collationDescriptor); ok {
			count++
		}
	}
	if rows.count() != count {
		t.Fatal("phantom or missing identities", rows.count(), count)
	}
	var previous uint32
	for _, row := range rows.anonymous {
		if len(row) != 12 {
			t.Fatal("row width", len(row))
		}
		id, err := row[0].OID()
		if err != nil || id <= previous {
			t.Fatal("private ordered identity", id, previous, err)
		}
		previous = id
		definition, ok := catalog.state.descriptors[ids.ObjectAddress{ClassID: CollationClassID, ObjectID: ids.OID(id)}].(*collationDescriptor)
		if !ok {
			t.Fatal("invented row", id)
		}
		for i, value := range row {
			if value.Type() != d.columns[i].Usage.Type {
				t.Fatal("concrete carrier", i, value)
			}
		}
		for i, want := range map[int]uint32{0: uint32(definition.oid), 2: uint32(definition.Namespace().ObjectID), 3: uint32(definition.owner.ID())} {
			if got, err := row[i].OID(); err != nil || got != want {
				t.Fatal("OID field", i, got, want, err)
			}
		}
		for i, want := range map[int]string{1: definition.name, 4: definition.provider} {
			if got, err := row[i].Text(); err != nil || got != want {
				t.Fatal("identity field", i, got, want, err)
			}
		}
		if got, err := row[5].Boolean(); err != nil || got != definition.deterministic {
			t.Fatal("deterministic", got, err)
		}
		if got, err := row[6].Integer(); err != nil || got != int64(definition.encoding) {
			t.Fatal("encoding", got, err)
		}
		for i, want := range []*string{definition.collate, definition.ctype, definition.locale, definition.rules, definition.version} {
			v := row[7+i]
			if v.IsNull() != (want == nil) {
				t.Fatal("NULL knownness", i, v, want)
			}
			if want != nil {
				if got, err := v.Text(); err != nil || got != *want {
					t.Fatal("exact metadata", i, got, *want, err)
				}
			}
		}
	}
	unchanged := func() {
		if e.state != state || e.nextOID != oid || e.nextRevision != revision || !maps.Equal(descriptors, state.descriptors) || !maps.Equal(names, state.names) || !maps.Equal(capturedDescriptors, catalog.state.descriptors) || !maps.Equal(capturedNames, catalog.state.names) {
			t.Fatal("capture mutated catalog/frontier")
		}
	}
	unchanged()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := capturePGCollation(ctx, catalog, ref, d); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published partial source", got, err)
	}
	unchanged()
	return rows
}

func TestPGCollationCapture(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	seedRows := pgCollationUnitCapture(t, e, base)
	if seedRows.count() != 7 {
		t.Fatal("canonical installed descriptors", seedRows.count())
	}
	stage := e.BeginCatalogStage(base)
	c := collationUnitDefinition(e, 90000, "pgc_capture")
	b := collationUnitDefinition(e, 90001, "pgc_empty_version")
	b.provider, b.encoding, b.collate, b.ctype, b.locale, b.version, b.semantics = "b", 6, nil, nil, collationText("C.UTF-8"), collationText(""), 811
	for _, d := range []*collationDescriptor{c, b} {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	collationUnitPublish(t, e, stage)
	before := e.CatalogRead()
	old := pgCollationUnitCapture(t, e, before)
	if old.count() != 9 {
		t.Fatal("configured identity population", old.count())
	}
	check := func(rows *querySource, name string) {
		t.Helper()
		for _, row := range rows.anonymous {
			id, _ := row[0].OID()
			if id == 90000 {
				got, err := row[1].Text()
				if err != nil || got != name || !row[9].IsNull() || !row[10].IsNull() || !row[11].IsNull() {
					t.Fatal("retained libc fields", got, name, err)
				}
			}
			if id == 90001 {
				version, err := row[11].Text()
				if row[11].IsNull() || err != nil || version != "" || !row[7].IsNull() || !row[8].IsNull() {
					t.Fatal("empty version is not NULL", row, err)
				}
			}
		}
	}
	check(old, "pgc_capture")
	next := *c
	next.name, next.revision = "pgc_capture_renamed", c.revision+1
	stage = e.BeginCatalogStage(before)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	private := pgCollationUnitCapture(t, e, e.stageRead(stage))
	check(private, "pgc_capture_renamed")
	check(pgCollationUnitCapture(t, e, before), "pgc_capture")
	collationUnitPublish(t, e, stage)
	check(pgCollationUnitCapture(t, e, e.CatalogRead()), "pgc_capture_renamed")
	check(old, "pgc_capture")
	if seedRows.count() != 7 {
		t.Fatal("old capture population changed")
	}
}
