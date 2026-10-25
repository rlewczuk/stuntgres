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

func pgAMUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2601}
	d, ok := e.Catalog.State.Relation(a)
	if !ok {
		t.Fatal("missing pg_am")
	}
	return d, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), a)
}

func pgAMUnitFacts(t *testing.T, source *exec.Source, d *catalog.Relation) [][]any {
	t.Helper()
	if len(source.Anonymous) != 4 {
		t.Fatal("access-method count", len(source.Anonymous))
	}
	got := make([][]any, len(source.Anonymous))
	for i, row := range source.Anonymous {
		if len(row) != 4 {
			t.Fatal("access-method width", len(row))
		}
		got[i] = make([]any, 4)
		for j, v := range row {
			if v.IsNull() || v.Type() != d.Columns[j].Usage.Type {
				t.Fatal("null/foreign cell", i, j, v)
			}
			var err error
			switch j {
			case 0:
				got[i][j], err = v.OID()
			case 2:
				got[i][j], err = v.ProcedureOID()
			default:
				got[i][j], err = v.Text()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	want := [][]any{{uint32(2), "heap", uint32(3), "t"}, {uint32(403), "btree", uint32(330), "i"}, {uint32(405), "hash", uint32(331), "i"}, {uint32(783), "gist", uint32(332), "i"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("access-method identities: got %v want %v", got, want)
	}
	return got
}

func TestPGAMCaptureOwnership(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, ref := pgAMUnitSource(t, e)
	checkDescriptor := func(view catalog.Read, d *catalog.Relation) {
		t.Helper()
		if d.Name() != "pg_am" || d.Namespace().ObjectID != 11 || d.RowType != 10015 || !d.History.HasIndexes || d.VirtualView || d.RelationKind != catalog.CatalogRelation || d.ColumnMeta.HighWater != 4 || len(d.Columns) != 4 || len(d.ColumnMeta.CatalogAttributes) != 4 {
			t.Fatal("descriptor", d)
		}
		for i, name := range []string{"oid", "amname", "amhandler", "amtype"} {
			id := []sqltype.ID{26, 19, 24, 18}[i]
			collation := uint32(0)
			if i == 1 {
				collation = 950
			}
			owned, ok := view.LookupType(id)
			c := d.Columns[i]
			if !ok || c.Name != name || c.Usage.Type != owned || c.Usage.Typmod != -1 || uint32(c.Usage.Collation.ID) != collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2601 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[name] != i {
				t.Fatal("column ownership", i, c)
			}
			a := d.ColumnMeta.CatalogAttributes[i]
			if !a.NotNull || a.Dimensions != 0 || uint32(a.Collation) != collation {
				t.Fatal("attribute facts", i, a)
			}
		}
		if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
			t.Fatal("PUBLIC SELECT", d.Security.ACL)
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10015})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10014})
		ownedRow, rowOK := view.LookupType(10015)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || row.Array != 10014 || array.SQLType.Element() != row.SQLType || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Owner != d.Owner || array.Owner != d.Owner {
			t.Fatal("row/array ownership", row, array)
		}
		if err := (RelationProvider).Validate(view, d); err != nil {
			t.Fatal(err)
		}
	}
	checkDescriptor(view, d)
	descriptors, names := maps.Collect(e.Catalog.State.Descriptors()), maps.Collect(e.Catalog.State.Names())
	rows, err := captureCatalogSourceLocked(e, context.Background(), view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Ref != ref || !maps.Equal(descriptors, maps.Collect(e.Catalog.State.Descriptors())) || !maps.Equal(names, maps.Collect(e.Catalog.State.Names())) {
		t.Fatal("capture mutated catalog/ref")
	}
	facts := pgAMUnitFacts(t, rows, d)
	second, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: d, Owner: (view).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGAM)
	if err != nil {
		t.Fatal(err)
	}
	rows.Anonymous[0][0] = sqltype.Null(d.Columns[0].Usage.Type)
	if !reflect.DeepEqual(pgAMUnitFacts(t, second, d), facts) {
		t.Fatal("captures share value backing")
	}
	rows.Anonymous[0] = nil
	if !reflect.DeepEqual(pgAMUnitFacts(t, second, d), facts) {
		t.Fatal("captures share row directory")
	}
	rows = second
	foreign := New().CatalogRead()
	badSources := []*catalog.Relation{nil, {Columns: d.Columns[:3]}, {Columns: append(append([]catalog.Column(nil), d.Columns...), d.Columns[0])}}
	for slot := range d.Columns {
		invalid := *d
		invalid.Columns = append([]catalog.Column(nil), d.Columns...)
		invalid.Columns[slot].Usage.Type, _ = foreign.LookupType(d.Columns[slot].Usage.Type.ID())
		badSources = append(badSources, &invalid)
		wrong := *d
		wrong.Columns = append([]catalog.Column(nil), d.Columns...)
		wrong.Columns[slot].Usage.Type = d.Columns[(slot+1)%4].Usage.Type
		badSources = append(badSources, &wrong)
	}
	for _, bad := range badSources {
		got, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: bad, Owner: (view).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGAM)
		message := "stuntgres: foreign access-method source type"
		if bad == nil || len(bad.Columns) != 4 {
			message = "stuntgres: invalid access-method source shape"
		}
		if got != nil || err == nil || err.Error() != message {
			t.Fatal("invalid source admitted", got, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("invariant gained SQLSTATE", err)
		}
	}
	for _, at := range []int32{1, 2, 5} {
		during, cancel := context.WithCancel(context.Background())
		var ctx context.Context = &cancelAtErrCheck{Context: during, cancel: cancel, at: at}
		if at == 1 {
			cancel()
			ctx = during
		}
		got, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: view, Ref: ref, Source: d, Owner: (view).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGAM)
		cancel()
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled capture", got, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("cancellation gained SQLSTATE", err)
		}
	}
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	fresh, freshRef := pgAMUnitSource(t, e)
	freshCatalog := e.CatalogRead()
	checkDescriptor(freshCatalog, fresh)
	for i := range d.Columns {
		if d.Columns[i].Usage.Type == fresh.Columns[i].Usage.Type {
			t.Fatal("generation not reowned", i)
		}
	}
	oldRow, _ := view.LookupType(10015)
	newRow, _ := freshCatalog.LookupType(10015)
	oldArray, _ := view.LookupType(10014)
	newArray, _ := freshCatalog.LookupType(10014)
	if d == fresh || oldRow == newRow || oldArray == newArray {
		t.Fatal("relation/row/array descriptor reused")
	}
	freshRows, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: freshCatalog, Ref: freshRef, Source: fresh, Owner: (freshCatalog).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGAM)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pgAMUnitFacts(t, freshRows, fresh), facts) || !reflect.DeepEqual(pgAMUnitFacts(t, rows, d), facts) {
		t.Fatal("replacement changed retained facts")
	}
	if got, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: freshCatalog, Ref: freshRef, Source: d, Owner: (freshCatalog).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGAM); got != nil || err == nil || err.Error() != "stuntgres: foreign access-method source type" {
		t.Fatal("old source accepted by fresh generation", got, err)
	}
}
