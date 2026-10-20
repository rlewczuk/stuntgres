package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgRangeUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3541}
	d, ok := e.catalog.state.Relation(a)
	if !ok {
		t.Fatal("missing pg_range")
	}
	return d, e.catalog.Domain.Ref(e.catalog.state.Epoch(), a)
}
func pgRangeUnitFacts() [][7]uint32 {
	return [][7]uint32{
		{3904, 23, 4451, 0, 1978, 3914, 3922}, {3906, 1700, 4532, 0, 3125, 0, 3924},
		{3908, 1114, 4533, 0, 3128, 0, 3929}, {3910, 1184, 4534, 0, 3127, 0, 3930},
		{3912, 1082, 4535, 0, 3122, 3915, 3925}, {3926, 20, 4536, 0, 3124, 3928, 3923},
	}
}
func pgRangeUnitBag(t *testing.T, rows *exec.Source, d *catalog.Relation) map[uint32][7]uint32 {
	t.Helper()
	if len(rows.Anonymous) != 6 {
		t.Fatal("range count", len(rows.Anonymous))
	}
	bag := map[uint32][7]uint32{}
	for _, row := range rows.Anonymous {
		if len(row) != 7 {
			t.Fatal("range width", len(row))
		}
		var fact [7]uint32
		for i, v := range row {
			if v.IsNull() || v.Type() != d.Columns[i].Usage.Type {
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
	view := e.CatalogRead()
	d, ref := pgRangeUnitSource(t, e)
	checkDescriptor := func(view catalog.Read, d *catalog.Relation) {
		t.Helper()
		if d.Name() != "pg_range" || d.Namespace().ObjectID != 11 || d.RowType != 10101 || !d.History.HasIndexes || d.RelationKind != catalog.CatalogRelation || d.ColumnMeta.HighWater != 7 || len(d.Columns) != 7 {
			t.Fatal("descriptor", d)
		}
		for i, name := range []string{"rngtypid", "rngsubtype", "rngmultitypid", "rngcollation", "rngsubopc", "rngcanonical", "rngsubdiff"} {
			id := sqltype.Oid
			if i >= 5 {
				id = sqltype.Regproc
			}
			owned, ok := view.LookupType(id)
			c := d.Columns[i]
			if !ok || c.Name != name || c.Usage.Type != owned || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 0 || c.Nullable || !c.NullabilityKnown || c.OriginTable != 3541 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[name] != i || owned.PostgreSQLTypeLength() != 4 {
				t.Fatal("column ownership", i, c)
			}
		}
		if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
			t.Fatal("PUBLIC read", d.Security.ACL)
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10101})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10100})
		ownedRow, rowOK := view.LookupType(10101)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || row.Array != 10100 || array.SQLType.Element() != row.SQLType || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Owner != d.Owner || array.Owner != d.Owner {
			t.Fatal("row/array ownership", row, array)
		}
		if err := (relationProvider{}).Validate(view, d); err != nil {
			t.Fatal(err)
		}
	}
	checkDescriptor(view, d)
	descriptors, names := maps.Collect(e.catalog.state.Descriptors()), maps.Collect(e.catalog.state.Names())
	rows, err := e.captureCatalogSourceLocked(context.Background(), view, e.roles.read(e.catalog.owner(), view, e.roles.published), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Ref != ref || !maps.Equal(descriptors, maps.Collect(e.catalog.state.Descriptors())) || !maps.Equal(names, maps.Collect(e.catalog.state.Names())) {
		t.Fatal("capture mutated catalog/ref")
	}
	bag := pgRangeUnitBag(t, rows, d)
	second, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: d, Owner: (view).Owner(), Runtime: engineCatalogRuntime{}}, capturePGRange)
	if err != nil {
		t.Fatal(err)
	}
	rows.Anonymous[0][0] = sqltype.Null(d.Columns[0].Usage.Type)
	if !reflect.DeepEqual(pgRangeUnitBag(t, second, d), bag) {
		t.Fatal("captures share value backing")
	}
	rows.Anonymous[0] = nil
	if !reflect.DeepEqual(pgRangeUnitBag(t, second, d), bag) {
		t.Fatal("captures share row directory")
	}
	// Keep the unmodified capture for the retained-generation proof.
	rows = second
	foreign := New().CatalogRead()
	badSources := []*catalog.Relation{nil, {Columns: d.Columns[:6]}, {Columns: append(append([]catalog.Column(nil), d.Columns...), d.Columns[0])}}
	for _, slot := range []int{0, 5} {
		invalid := *d
		invalid.Columns = append([]catalog.Column(nil), d.Columns...)
		invalid.Columns[slot].Usage.Type, _ = foreign.LookupType(d.Columns[slot].Usage.Type.ID())
		badSources = append(badSources, &invalid)
	}
	wrong := *d
	wrong.Columns = append([]catalog.Column(nil), d.Columns...)
	wrong.Columns[5].Usage.Type = d.Columns[0].Usage.Type
	badSources = append(badSources, &wrong)
	for _, bad := range badSources {
		got, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: bad, Owner: (view).Owner(), Runtime: engineCatalogRuntime{}}, capturePGRange)
		message := "stuntgres: foreign range source type"
		if bad == nil || len(bad.Columns) != 7 {
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
		got, err := captureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: view, Ref: ref, Source: d, Owner: (view).Owner(), Runtime: engineCatalogRuntime{}}, capturePGRange)
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
	for i := range d.Columns {
		if d.Columns[i].Usage.Type == fresh.Columns[i].Usage.Type {
			t.Fatal("generation not reowned", i)
		}
	}
	oldRow, _ := view.LookupType(10101)
	newRow, _ := freshCatalog.LookupType(10101)
	if d == fresh || oldRow == newRow {
		t.Fatal("relation/row descriptor reused")
	}
	freshRows, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: freshCatalog, Ref: freshRef, Source: fresh, Owner: (freshCatalog).Owner(), Runtime: engineCatalogRuntime{}}, capturePGRange)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pgRangeUnitBag(t, freshRows, fresh), bag) || !reflect.DeepEqual(pgRangeUnitBag(t, rows, d), bag) {
		t.Fatal("replacement changed retained facts")
	}
	if got, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: freshCatalog, Ref: freshRef, Source: d, Owner: (freshCatalog).Owner(), Runtime: engineCatalogRuntime{}}, capturePGRange); got != nil || err == nil || err.Error() != "stuntgres: foreign range source type" {
		t.Fatal("old source accepted by fresh generation", got, err)
	}
}
