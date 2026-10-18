package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgRangeUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ObjectRef) {
	t.Helper()
	a := ObjectAddress{ClassID: RelationClassID, ObjectID: 3541}
	d, ok := e.state.descriptors[a].(*relationDescriptor)
	if !ok {
		t.Fatal("missing pg_range")
	}
	return d, e.domain.Ref(e.state.epoch, a)
}
func pgRangeUnitFacts() [][7]uint32 {
	return [][7]uint32{
		{3904, 23, 4451, 0, 1978, 3914, 3922}, {3906, 1700, 4532, 0, 3125, 0, 3924},
		{3908, 1114, 4533, 0, 3128, 0, 3929}, {3910, 1184, 4534, 0, 3127, 0, 3930},
		{3912, 1082, 4535, 0, 3122, 3915, 3925}, {3926, 20, 4536, 0, 3124, 3928, 3923},
	}
}
func pgRangeUnitBag(t *testing.T, rows *querySource, d *relationDescriptor) map[uint32][7]uint32 {
	t.Helper()
	if len(rows.anonymous) != 6 {
		t.Fatal("range count", len(rows.anonymous))
	}
	bag := map[uint32][7]uint32{}
	for _, row := range rows.anonymous {
		if len(row) != 7 {
			t.Fatal("range width", len(row))
		}
		var fact [7]uint32
		for i, v := range row {
			if v.IsNull() || v.Type() != d.columns[i].Usage.Type {
				t.Fatal("null/foreign cell", i, v)
			}
			var err error
			if i < 5 {
				fact[i], err = v.OID()
			} else {
				fact[i], err = v.ProcedureOID()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, ok := bag[fact[0]]; ok {
			t.Fatal("duplicate range", fact)
		}
		bag[fact[0]] = fact
	}
	for _, want := range pgRangeUnitFacts() {
		if bag[want[0]] != want {
			t.Fatal("range linkage", bag, want)
		}
	}
	return bag
}
func TestPGRangeCaptureOwnership(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	d, ref := pgRangeUnitSource(t, e)
	checkDescriptor := func(catalog CatalogRead, d *relationDescriptor) {
		t.Helper()
		if d.name != "pg_range" || d.namespace.ObjectID != 11 || d.rowType != 10101 || !d.hasIndexes || d.relationKind != catalogRelation || d.attributeHighWater != 7 || len(d.columns) != 7 {
			t.Fatal("descriptor", d)
		}
		for i, name := range []string{"rngtypid", "rngsubtype", "rngmultitypid", "rngcollation", "rngsubopc", "rngcanonical", "rngsubdiff"} {
			id := sqltype.Oid
			if i >= 5 {
				id = sqltype.Regproc
			}
			owned, ok := catalog.LookupType(id)
			c := d.columns[i]
			if !ok || c.Name != name || c.Usage.Type != owned || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 0 || c.Nullable || !c.NullabilityKnown || c.OriginTable != 3541 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[name] != i || owned.PostgreSQLTypeLength() != 4 {
				t.Fatal("column ownership", i, c)
			}
		}
		if len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
			t.Fatal("PUBLIC read", d.acl)
		}
		row := catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: 10101}].(*typeDescriptor)
		array := catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: 10100}].(*typeDescriptor)
		ownedRow, rowOK := catalog.LookupType(10101)
		ownedArray, arrayOK := catalog.LookupArray(ownedRow)
		if !rowOK || !arrayOK || row.typ != ownedRow || array.typ != ownedArray || row.array != 10100 || array.typ.Element() != row.typ || row.owningRelation != d.address || array.owningRelation != d.address || row.owner != d.owner || array.owner != d.owner {
			t.Fatal("row/array ownership", row, array)
		}
		if err := (relationProvider{}).Validate(catalog, d); err != nil {
			t.Fatal(err)
		}
	}
	checkDescriptor(catalog, d)
	descriptors, names := maps.Clone(e.state.descriptors), maps.Clone(e.state.names)
	rows, err := e.captureCatalogSourceLocked(context.Background(), catalog, e.roleRead(catalog, e.roles), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	if rows.ref != ref || !maps.Equal(descriptors, e.state.descriptors) || !maps.Equal(names, e.state.names) {
		t.Fatal("capture mutated catalog/ref")
	}
	bag := pgRangeUnitBag(t, rows, d)
	second, err := capturePGRange(context.Background(), catalog, ref, d)
	if err != nil {
		t.Fatal(err)
	}
	rows.anonymous[0][0] = sqltype.Null(d.columns[0].Usage.Type)
	if !reflect.DeepEqual(pgRangeUnitBag(t, second, d), bag) {
		t.Fatal("captures share value backing")
	}
	rows.anonymous[0] = nil
	if !reflect.DeepEqual(pgRangeUnitBag(t, second, d), bag) {
		t.Fatal("captures share row directory")
	}
	// Keep the unmodified capture for the retained-generation proof.
	rows = second
	foreign := New().CatalogRead()
	badSources := []*relationDescriptor{nil, {columns: d.columns[:6]}, {columns: append(append([]ColumnDescriptor(nil), d.columns...), d.columns[0])}}
	for _, slot := range []int{0, 5} {
		invalid := *d
		invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
		invalid.columns[slot].Usage.Type, _ = foreign.LookupType(d.columns[slot].Usage.Type.ID())
		badSources = append(badSources, &invalid)
	}
	wrong := *d
	wrong.columns = append([]ColumnDescriptor(nil), d.columns...)
	wrong.columns[5].Usage.Type = d.columns[0].Usage.Type
	badSources = append(badSources, &wrong)
	for _, bad := range badSources {
		got, err := capturePGRange(context.Background(), catalog, ref, bad)
		message := "stuntgres: foreign range source type"
		if bad == nil || len(bad.columns) != 7 {
			message = "stuntgres: invalid range source shape"
		}
		if got != nil || err == nil || err.Error() != message {
			t.Fatal("invalid source admitted", got, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("invariant gained SQLSTATE", err)
		}
	}
	for _, at := range []int32{1, 5, 9} {
		during, cancel := context.WithCancel(context.Background())
		var ctx context.Context = &cancelAtErrCheck{Context: during, cancel: cancel, at: at}
		if at == 1 {
			cancel()
			ctx = during
		}
		got, err := capturePGRange(ctx, catalog, ref, d)
		cancel()
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled capture", got, err)
		}
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh, freshRef := pgRangeUnitSource(t, e)
	freshCatalog := e.CatalogRead()
	checkDescriptor(freshCatalog, fresh)
	for i := range d.columns {
		if d.columns[i].Usage.Type == fresh.columns[i].Usage.Type {
			t.Fatal("generation not reowned", i)
		}
	}
	oldRow, _ := catalog.LookupType(10101)
	newRow, _ := freshCatalog.LookupType(10101)
	if d == fresh || oldRow == newRow {
		t.Fatal("relation/row descriptor reused")
	}
	freshRows, err := capturePGRange(context.Background(), freshCatalog, freshRef, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pgRangeUnitBag(t, freshRows, fresh), bag) || !reflect.DeepEqual(pgRangeUnitBag(t, rows, d), bag) {
		t.Fatal("replacement changed retained facts")
	}
	if got, err := capturePGRange(context.Background(), freshCatalog, freshRef, d); got != nil || err == nil || err.Error() != "stuntgres: foreign range source type" {
		t.Fatal("old source accepted by fresh generation", got, err)
	}
}
