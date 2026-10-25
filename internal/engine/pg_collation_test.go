package engine

import (
	"context"
	"errors"
	"maps"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func pgCollationUnitSource(t *testing.T, e *Engine, catalog catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3456}
	d, ok := catalog.Relation(address)
	if !ok {
		t.Fatal("missing pg_collation")
	}
	return d, e.Catalog.Domain.Ref(catalog.Epoch(), address)
}

func TestPGCollationShapeAndOwnership(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, _ := pgCollationUnitSource(t, e, view)
	owner := e.Roles().LookupRef("postgres")
	if d.Name() != "pg_collation" || d.Namespace().ObjectID != 11 || d.Owner != owner || d.RelationKind != catalog.CatalogRelation || d.VirtualView || d.ColumnMeta.HighWater != 12 || len(d.Columns) != 12 {
		t.Fatal("catalog identity", d)
	}
	names := [...]string{"oid", "collname", "collnamespace", "collowner", "collprovider", "collisdeterministic", "collencoding", "collcollate", "collctype", "colllocale", "collicurules", "collversion"}
	typeIDs := [...]sqltype.ID{26, 19, 26, 26, 18, 16, 23, 25, 25, 25, 25, 25}
	collations := [...]uint32{0, 950, 0, 0, 0, 0, 0, 950, 950, 950, 950, 950}
	for i, column := range d.Columns {
		typ, ok := view.LookupType(typeIDs[i])
		if !ok || column.Name != names[i] || column.Usage.Type != typ || column.Usage.Typmod != -1 || uint32(column.Usage.Collation.ID) != collations[i] || column.Nullable != (i >= 7) || !column.NullabilityKnown || column.OriginTable != 3456 || column.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[names[i]] != i {
			t.Error("release attribute", i+1, column)
		}
	}
	if err := (relationProvider).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if !d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Grantor != owner || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.Security.ACL)
	}
	row, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)})
	if !ok {
		t.Fatal("owned row type", d.RowType)
	}
	array, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if !ok {
		t.Fatal("owned row array", row.Array)
	}
	ownedRow, rowOK := view.LookupType(d.RowType)
	ownedArray, arrayOK := view.LookupArray(ownedRow)
	if !rowOK || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || array.SQLType.Element() != row.SQLType || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Owner != owner || array.Owner != owner {
		t.Fatal("relational row/array ownership", row, array)
	}
}

func pgCollationUnitCapture(t *testing.T, e *Engine, view catalog.Read) *exec.Source {
	t.Helper()
	d, ref := pgCollationUnitSource(t, e, view)
	state, oid, revision := e.Catalog.State, e.Catalog.NextOID(), e.Catalog.NextRevision()
	descriptors, names := maps.Collect(state.Descriptors()), maps.Collect(state.Names())
	capturedDescriptors, capturedNames := maps.Collect(view.State().Descriptors()), maps.Collect(view.State().Names())
	e.Mu.Lock()
	rows, err := e.captureCatalogSourceLocked(context.Background(), view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ref, d)
	e.Mu.Unlock()
	if err != nil || rows == nil || rows.Ref != ref {
		t.Fatal("capture", rows, err)
	}
	count := 0
	for _, descriptor := range view.Descriptors() {
		if _, ok := descriptor.(*catalog.Collation); ok {
			count++
		}
	}
	if rows.Count() != count {
		t.Fatal("phantom or missing identities", rows.Count(), count)
	}
	var previous uint32
	for _, row := range rows.Anonymous {
		if len(row) != 12 {
			t.Fatal("row width", len(row))
		}
		id, err := row[0].OID()
		if err != nil || id <= previous {
			t.Fatal("private ordered identity", id, previous, err)
		}
		previous = id
		definition, ok := view.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)})
		if !ok {
			t.Fatal("invented row", id)
		}
		for i, value := range row {
			if value.Type() != d.Columns[i].Usage.Type {
				t.Fatal("concrete carrier", i, value)
			}
		}
		for i, want := range map[int]uint32{0: uint32(definition.OID), 2: uint32(definition.Namespace().ObjectID), 3: uint32(definition.Owner.ID())} {
			if got, err := row[i].OID(); err != nil || got != want {
				t.Fatal("OID field", i, got, want, err)
			}
		}
		for i, want := range map[int]string{1: definition.Name(), 4: definition.Provider} {
			if got, err := row[i].Text(); err != nil || got != want {
				t.Fatal("identity field", i, got, want, err)
			}
		}
		if got, err := row[5].Boolean(); err != nil || got != definition.Deterministic {
			t.Fatal("deterministic", got, err)
		}
		if got, err := row[6].Integer(); err != nil || got != int64(definition.Encoding) {
			t.Fatal("encoding", got, err)
		}
		for i, want := range []*string{definition.Collate, definition.CType, definition.Locale, definition.Rules, definition.Version} {
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
		if e.Catalog.State != state || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || !maps.Equal(descriptors, maps.Collect(state.Descriptors())) || !maps.Equal(names, maps.Collect(state.Names())) || !maps.Equal(capturedDescriptors, maps.Collect(view.State().Descriptors())) || !maps.Equal(capturedNames, maps.Collect(view.State().Names())) {
			t.Fatal("capture mutated catalog/frontier")
		}
	}
	unchanged()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: view, Ref: ref, Source: d, Owner: (view).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGCollation); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published partial source", got, err)
	}
	unchanged()
	return rows
}

func TestPGCollationCapture(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	seedRows := pgCollationUnitCapture(t, e, base)
	if seedRows.Count() != 7 {
		t.Fatal("canonical installed descriptors", seedRows.Count())
	}
	stage := e.BeginCatalogStage(base)
	c := collationUnitDefinition(e, 90000, "pgc_capture")
	b := collationUnitDefinition(e, 90001, "pgc_empty_version")
	b.Provider, b.Encoding, b.Collate, b.CType, b.Locale, b.Version, b.Semantics = "b", 6, nil, nil, catalog.CollationText("C.UTF-8"), catalog.CollationText(""), 811
	for _, d := range []*catalog.Collation{c, b} {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	collationUnitPublish(t, e, stage)
	before := e.CatalogRead()
	old := pgCollationUnitCapture(t, e, before)
	if old.Count() != 9 {
		t.Fatal("configured identity population", old.Count())
	}
	check := func(rows *exec.Source, name string) {
		t.Helper()
		for _, row := range rows.Anonymous {
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
	next.ObjectName, next.ObjectRevision = "pgc_capture_renamed", c.ObjectRevision+1
	stage = e.BeginCatalogStage(before)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	private := pgCollationUnitCapture(t, e, e.StageRead(stage))
	check(private, "pgc_capture_renamed")
	check(pgCollationUnitCapture(t, e, before), "pgc_capture")
	collationUnitPublish(t, e, stage)
	check(pgCollationUnitCapture(t, e, e.CatalogRead()), "pgc_capture_renamed")
	check(old, "pgc_capture")
	if seedRows.Count() != 7 {
		t.Fatal("old capture population changed")
	}
}
