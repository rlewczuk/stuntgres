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

func pgOpclassUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2616}
	d, ok := e.catalog.state.Relation(a)
	if !ok {
		t.Fatal("missing pg_opclass")
	}
	return d, e.catalog.domain.Ref(e.catalog.state.epoch, a)
}

func pgOpclassUnitBag(t *testing.T, rows *querySource, d *relationDescriptor) map[uint32][]any {
	t.Helper()
	if len(rows.anonymous) != 62 {
		t.Fatal("class count", len(rows.anonymous))
	}
	bag := map[uint32][]any{}
	for _, row := range rows.anonymous {
		if len(row) != 9 {
			t.Fatal("width", len(row))
		}
		values := make([]any, 9)
		for i, v := range row {
			if v.IsNull() || v.Type() != d.columns[i].Usage.Type {
				t.Fatal("unowned/null cell", i, v)
			}
			var err error
			switch i {
			case 2:
				values[i], err = v.Text()
			case 7:
				values[i], err = v.Boolean()
			default:
				values[i], err = v.OID()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		oid := values[0].(uint32)
		if oid == 0 || bag[oid] != nil {
			t.Fatal("duplicate identity", oid)
		}
		bag[oid] = values
	}
	return bag
}

func TestPGOpclassCaptureOwnership(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	d, ref := pgOpclassUnitSource(t, e)
	if d.name != "pg_opclass" || d.namespace.ObjectID != 11 || d.rowType != 10013 || !d.hasIndexes || d.relationKind != catalogRelation || d.attributeHighWater != 9 || len(d.columns) != 9 {
		t.Fatal("descriptor", d)
	}
	for i, name := range []string{"oid", "opcmethod", "opcname", "opcnamespace", "opcowner", "opcfamily", "opcintype", "opcdefault", "opckeytype"} {
		id := sqltype.Oid
		collation := uint32(0)
		if i == 2 {
			id = sqltype.Name
			collation = 950
		}
		if i == 7 {
			id = sqltype.Bool
		}
		owned, ok := catalog.LookupType(id)
		c := d.columns[i]
		if !ok || c.Name != name || c.Usage.Type != owned || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2616 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[name] != i {
			t.Fatal("column", i, c)
		}
	}
	if len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC read", d.acl)
	}
	if err := (relationProvider{}).Validate(catalog, d); err != nil {
		t.Fatal(err)
	}
	descriptors, names := maps.Clone(e.catalog.state.descriptors), maps.Clone(e.catalog.state.names)
	rows, err := e.captureCatalogSourceLocked(context.Background(), catalog, e.roleRead(catalog, e.roles), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	if rows.ref != ref || !maps.Equal(descriptors, e.catalog.state.descriptors) || !maps.Equal(names, e.catalog.state.names) {
		t.Fatal("capture changed catalog/ref")
	}
	bag := pgOpclassUnitBag(t, rows, d)
	for i := range sqltype.IndexClassMetadataCount() {
		m, _ := sqltype.IndexClassMetadataAt(i)
		want := []any{m.OID, m.Method, m.Name, uint32(11), uint32(10), m.Family, m.Input, m.Default, m.KeyType}
		if !reflect.DeepEqual(bag[m.OID], want) {
			t.Fatal("projection", m, bag[m.OID])
		}
	}
	foreign, _ := New().CatalogRead().LookupType(sqltype.Oid)
	invalid := *d
	invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
	invalid.columns[0].Usage.Type = foreign
	for _, source := range []*relationDescriptor{&invalid, {columns: d.columns[:8]}} {
		if rows, err := capturePGOpclass(context.Background(), catalog, ref, source); rows != nil || err == nil {
			t.Fatal("invalid source accepted", rows, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	during, cancelDuring := context.WithCancel(context.Background())
	defer cancelDuring()
	for _, ctx := range []context.Context{ctx, &cancelAtErrCheck{Context: during, cancel: cancelDuring, at: 5}} {
		if rows, err := capturePGOpclass(ctx, catalog, ref, d); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled capture", rows, err)
		}
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh, freshRef := pgOpclassUnitSource(t, e)
	freshRows, err := capturePGOpclass(context.Background(), e.CatalogRead(), freshRef, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.columns[0].Usage.Type == d.columns[0].Usage.Type {
		t.Fatal("generation not reowned")
	}
	if !reflect.DeepEqual(pgOpclassUnitBag(t, freshRows, fresh), bag) || !reflect.DeepEqual(pgOpclassUnitBag(t, rows, d), bag) {
		t.Fatal("replacement changed immutable facts/old capture")
	}
	if rows, err := capturePGOpclass(context.Background(), e.CatalogRead(), freshRef, d); rows != nil || err == nil {
		t.Fatal("old descriptors accepted by new generation", rows, err)
	}
}

func TestPGOpclassIndexIdentity(t *testing.T) {
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	domain, err := sqltype.Domain(48000, "identity_domain", "public.identity_domain", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	enum, err := sqltype.Enum(48001, "identity_enum", "public.identity_enum", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		id           sqltype.ID
		class, input string
		method       indexMethod
		oid          ids.OID
	}{
		{sqltype.Int4, "int4_ops", "1", btreeMethod, 1978},
		{sqltype.Int4, "int4_ops", "1", hashMethod, 10020},
		{sqltype.VarChar, "varchar_ops", "one", btreeMethod, 10044},
		{sqltype.VarChar, "varchar_pattern_ops", "one", hashMethod, 10057},
		{sqltype.Name, "name_ops", "one", btreeMethod, 10028},
		{sqltype.Name, "name_ops", "one", hashMethod, 10029},
		{48000, "int4_ops", "1", btreeMethod, 1978},
		{48001, "enum_ops", "a", hashMethod, 10070},
	} {
		typ, _ := e.CatalogRead().LookupType(tc.id)
		if tc.id == 48000 {
			typ = domain
		}
		if tc.id == 48001 {
			typ = enum
		}
		class, ok := tc.method.lookupClass(tc.class)
		if !ok || class.OID() != uint32(tc.oid) {
			t.Fatal("class identity", tc, class)
		}
		parent := &relationDescriptor{address: ids.ObjectAddress{ids.RelationClassID, ids.OID(48100 + i*2), 0}, namespace: ids.ObjectAddress{ids.NamespaceClassID, ids.PublicNamespaceOID, 0}, name: "identity_parent", relationKind: tableRelation, attributes: []int32{1}, columns: []ColumnDescriptor{{Name: "k", Usage: typ.Usage()}}, index: map[string]int{"k": 0}}
		index := &relationDescriptor{address: ids.ObjectAddress{ids.RelationClassID, ids.OID(48101 + i*2), 0}, namespace: parent.namespace, name: "identity_index", relationKind: indexRelation, columns: parent.columns, attributes: []int32{1}, sqlIndex: &indexDefinition{parent: parent.address, method: tc.method, keys: []indexKey{{attribute: 1, class: class, explicitClass: true}}, ready: true, valid: true, live: true}}
		e.catalog.state.descriptors[parent.address], e.catalog.state.descriptors[index.address] = parent, index
		value, err := typ.Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		root, err := buildIndexRoot(&EvalContext{Context: context.Background()}, parent, index.sqlIndex, &relationStorage{versions: []rowVersion{{row: 1, version: 1, born: 1, values: []sqltype.Value{value}}}}, nil)
		if err != nil || root == nil || (root.hash != nil) != (tc.method == hashMethod) {
			t.Fatal("real root", tc, root, err)
		}
		catalog := e.CatalogRead()
		m, _, err := projectIndexMetadata(context.Background(), catalog, e.catalog.domain.Ref(catalog.Epoch(), index.address))
		if err != nil || !reflect.DeepEqual(m.IndClass, []ids.OID{tc.oid}) {
			t.Fatal("projected index identity", tc, m.IndClass, err)
		}
		d, ref := pgOpclassUnitSource(t, e)
		rows, err := capturePGOpclass(context.Background(), catalog, ref, d)
		if err != nil {
			t.Fatal(err)
		}
		method := uint32(403)
		if tc.method == hashMethod {
			method = 405
		}
		if fact := pgOpclassUnitBag(t, rows, d)[uint32(tc.oid)]; fact == nil || fact[1] != method {
			t.Fatal("catalog/index method mismatch", tc, fact)
		}
	}
}
