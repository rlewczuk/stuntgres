package engine

import (
	"context"
	"encoding/csv"
	"errors"
	"maps"
	"os"
	"reflect"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgOperatorUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ObjectRef) {
	t.Helper()
	a := ObjectAddress{ClassID: RelationClassID, ObjectID: 2617}
	d, ok := e.state.descriptors[a].(*relationDescriptor)
	if !ok {
		t.Fatal("missing pg_operator")
	}
	return d, ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: a}
}

func pgOperatorReleaseRows(t *testing.T, name string, width int) [][]string {
	t.Helper()
	f, err := os.Open("../../docs/design/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma, r.FieldsPerRecord, r.LazyQuotes = '\t', width, true
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows[1:]
}

func pgOperatorUnitBag(t *testing.T, rows *querySource, source *relationDescriptor) map[uint32][]string {
	t.Helper()
	bag := map[uint32][]string{}
	for _, row := range rows.anonymous {
		if len(row) != 15 || cap(row) != 15 {
			t.Fatal("row shape", len(row), cap(row))
		}
		fields := make([]string, 15)
		var oid uint32
		for i, value := range row {
			if value.IsNull() || value.Type() != source.columns[i].Usage.Type {
				t.Fatal("unowned/null cell", i, value)
			}
			var err error
			switch i {
			case 1, 4:
				fields[i], err = value.Text()
			case 5, 6:
				var flag bool
				flag, err = value.Boolean()
				fields[i] = "f"
				if flag {
					fields[i] = "t"
				}
			default:
				var id uint32
				if i >= 12 {
					id, err = value.ProcedureOID()
				} else {
					id, err = value.OID()
				}
				fields[i] = strconv.FormatUint(uint64(id), 10)
				if i == 0 {
					oid = id
				}
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if oid == 0 || bag[oid] != nil {
			t.Fatal("duplicate identity", oid)
		}
		bag[oid] = fields
	}
	return bag
}

func TestPGOperatorCaptureOwnership(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	d, ref := pgOperatorUnitSource(t, e)
	if d.rowType != 10009 || !d.hasIndexes || d.attributeHighWater != 15 || d.namespace.ObjectID != 11 || len(d.columns) != 15 {
		t.Fatal("relation facts", d)
	}
	for i, name := range []string{"oid", "oprname", "oprnamespace", "oprowner", "oprkind", "oprcanmerge", "oprcanhash", "oprleft", "oprright", "oprresult", "oprcom", "oprnegate", "oprcode", "oprrest", "oprjoin"} {
		id, collation := sqltype.Oid, uint32(0)
		switch i {
		case 1:
			id, collation = sqltype.Name, 950
		case 4:
			id = sqltype.Char
		case 5, 6:
			id = sqltype.Bool
		case 12, 13, 14:
			id = sqltype.Regproc
		}
		owned, ok := catalog.LookupType(id)
		c := d.columns[i]
		if !ok || c.Name != name || c.Usage.Type != owned || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2617 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[name] != i {
			t.Fatal("column", i, c)
		}
	}
	if len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.acl)
	}
	for _, id := range []sqltype.ID{10009, 10008} {
		row := catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)}].(*typeDescriptor)
		if row.owningRelation != d.address || row.owner != d.owner {
			t.Fatal("row/array ownership", id, row)
		}
	}
	if err := (relationProvider{}).Validate(catalog, d); err != nil {
		t.Fatal(err)
	}
	descriptors, names := maps.Clone(e.state.descriptors), maps.Clone(e.state.names)
	rows, err := e.captureCatalogSourceLocked(context.Background(), catalog, e.roleRead(catalog, e.roles), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	if rows.ref != ref || !maps.Equal(descriptors, e.state.descriptors) || !maps.Equal(names, e.state.names) {
		t.Fatal("capture mutated catalog/ref")
	}
	bag := pgOperatorUnitBag(t, rows, d)
	want := map[uint32][]string{}
	for _, fields := range pgOperatorReleaseRows(t, "pg-operator-pg18-supported.tsv", 15) {
		id, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		want[uint32(id)] = fields
	}
	if len(want) != 545 || !reflect.DeepEqual(bag, want) {
		t.Fatal("capture differs from independent release bag")
	}
	// Full-cap row slices cannot append into their neighbours; cells are distinct.
	first := rows.anonymous[1][0]
	rows.anonymous[0] = append(rows.anonymous[0], rows.anonymous[0][0])
	if !reflect.DeepEqual(rows.anonymous[1][0], first) {
		t.Fatal("row alias")
	}
	rows.anonymous[0] = rows.anonymous[0][:15:15]
	foreign, _ := New().CatalogRead().LookupType(sqltype.Oid)
	invalid := *d
	invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
	invalid.columns[0].Usage.Type = foreign
	for _, source := range []*relationDescriptor{&invalid, {columns: d.columns[:14]}} {
		if got, err := capturePGOperator(context.Background(), catalog, ref, source); got != nil || err == nil {
			t.Fatal("invalid source accepted", got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	during, cancelDuring := context.WithCancel(context.Background())
	defer cancelDuring()
	for _, c := range []context.Context{ctx, &cancelAtErrCheck{Context: during, cancel: cancelDuring, at: 5}} {
		if got, err := capturePGOperator(c, catalog, ref, d); got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled capture", got, err)
		}
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh, freshRef := pgOperatorUnitSource(t, e)
	freshRows, err := capturePGOperator(context.Background(), e.CatalogRead(), freshRef, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.columns[0].Usage.Type == d.columns[0].Usage.Type || !reflect.DeepEqual(pgOperatorUnitBag(t, freshRows, fresh), want) || !reflect.DeepEqual(pgOperatorUnitBag(t, rows, d), want) {
		t.Fatal("replacement changed facts/old capture or retained generation")
	}
	if got, err := capturePGOperator(context.Background(), e.CatalogRead(), freshRef, d); got != nil || err == nil {
		t.Fatal("foreign generation accepted", got, err)
	}
}
