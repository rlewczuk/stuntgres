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

func pgAMUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ObjectRef) {
	t.Helper()
	a := ObjectAddress{ClassID: RelationClassID, ObjectID: 2601}
	d, ok := e.state.descriptors[a].(*relationDescriptor)
	if !ok {
		t.Fatal("missing pg_am")
	}
	return d, ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: a}
}

func pgAMUnitFacts(t *testing.T, source *querySource, d *relationDescriptor) [][]any {
	t.Helper()
	if len(source.anonymous) != 4 {
		t.Fatal("access-method count", len(source.anonymous))
	}
	got := make([][]any, len(source.anonymous))
	for i, row := range source.anonymous {
		if len(row) != 4 {
			t.Fatal("access-method width", len(row))
		}
		got[i] = make([]any, 4)
		for j, v := range row {
			if v.IsNull() || v.Type() != d.columns[j].Usage.Type {
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
	catalog := e.CatalogRead()
	d, ref := pgAMUnitSource(t, e)
	checkDescriptor := func(catalog CatalogRead, d *relationDescriptor) {
		t.Helper()
		if d.name != "pg_am" || d.namespace.ObjectID != 11 || d.rowType != 10015 || !d.hasIndexes || d.virtualView || d.relationKind != catalogRelation || d.attributeHighWater != 4 || len(d.columns) != 4 || len(d.catalogAttributes) != 4 {
			t.Fatal("descriptor", d)
		}
		for i, name := range []string{"oid", "amname", "amhandler", "amtype"} {
			id := []sqltype.ID{26, 19, 24, 18}[i]
			collation := uint32(0)
			if i == 1 {
				collation = 950
			}
			owned, ok := catalog.LookupType(id)
			c := d.columns[i]
			if !ok || c.Name != name || c.Usage.Type != owned || c.Usage.Typmod != -1 || uint32(c.Usage.Collation.ID) != collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2601 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[name] != i {
				t.Fatal("column ownership", i, c)
			}
			a := d.catalogAttributes[i]
			if !a.notNull || a.dimensions != 0 || uint32(a.collation) != collation {
				t.Fatal("attribute facts", i, a)
			}
		}
		if len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
			t.Fatal("PUBLIC SELECT", d.acl)
		}
		row := catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: 10015}].(*typeDescriptor)
		array := catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: 10014}].(*typeDescriptor)
		ownedRow, rowOK := catalog.LookupType(10015)
		ownedArray, arrayOK := catalog.LookupArray(ownedRow)
		if !rowOK || !arrayOK || row.typ != ownedRow || array.typ != ownedArray || row.array != 10014 || array.typ.Element() != row.typ || row.owningRelation != d.address || array.owningRelation != d.address || row.owner != d.owner || array.owner != d.owner {
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
	facts := pgAMUnitFacts(t, rows, d)
	second, err := capturePGAM(context.Background(), catalog, ref, d)
	if err != nil {
		t.Fatal(err)
	}
	rows.anonymous[0][0] = sqltype.Null(d.columns[0].Usage.Type)
	if !reflect.DeepEqual(pgAMUnitFacts(t, second, d), facts) {
		t.Fatal("captures share value backing")
	}
	rows.anonymous[0] = nil
	if !reflect.DeepEqual(pgAMUnitFacts(t, second, d), facts) {
		t.Fatal("captures share row directory")
	}
	rows = second
	foreign := New().CatalogRead()
	badSources := []*relationDescriptor{nil, {columns: d.columns[:3]}, {columns: append(append([]ColumnDescriptor(nil), d.columns...), d.columns[0])}}
	for slot := range d.columns {
		invalid := *d
		invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
		invalid.columns[slot].Usage.Type, _ = foreign.LookupType(d.columns[slot].Usage.Type.ID())
		badSources = append(badSources, &invalid)
		wrong := *d
		wrong.columns = append([]ColumnDescriptor(nil), d.columns...)
		wrong.columns[slot].Usage.Type = d.columns[(slot+1)%4].Usage.Type
		badSources = append(badSources, &wrong)
	}
	for _, bad := range badSources {
		got, err := capturePGAM(context.Background(), catalog, ref, bad)
		message := "stuntgres: foreign access-method source type"
		if bad == nil || len(bad.columns) != 4 {
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
		got, err := capturePGAM(ctx, catalog, ref, d)
		cancel()
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled capture", got, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("cancellation gained SQLSTATE", err)
		}
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh, freshRef := pgAMUnitSource(t, e)
	freshCatalog := e.CatalogRead()
	checkDescriptor(freshCatalog, fresh)
	for i := range d.columns {
		if d.columns[i].Usage.Type == fresh.columns[i].Usage.Type {
			t.Fatal("generation not reowned", i)
		}
	}
	oldRow, _ := catalog.LookupType(10015)
	newRow, _ := freshCatalog.LookupType(10015)
	oldArray, _ := catalog.LookupType(10014)
	newArray, _ := freshCatalog.LookupType(10014)
	if d == fresh || oldRow == newRow || oldArray == newArray {
		t.Fatal("relation/row/array descriptor reused")
	}
	freshRows, err := capturePGAM(context.Background(), freshCatalog, freshRef, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pgAMUnitFacts(t, freshRows, fresh), facts) || !reflect.DeepEqual(pgAMUnitFacts(t, rows, d), facts) {
		t.Fatal("replacement changed retained facts")
	}
	if got, err := capturePGAM(context.Background(), freshCatalog, freshRef, d); got != nil || err == nil || err.Error() != "stuntgres: foreign access-method source type" {
		t.Fatal("old source accepted by fresh generation", got, err)
	}
}
