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

func pgOpclassUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ObjectRef) {
	t.Helper()
	a := ObjectAddress{ClassID: RelationClassID, ObjectID: 2616}
	d, ok := e.state.descriptors[a].(*relationDescriptor)
	if !ok {
		t.Fatal("missing pg_opclass")
	}
	return d, ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: a}
}

func pgOpclassUnitBag(t *testing.T, rows *querySource, d *relationDescriptor) map[uint32][]any {
	t.Helper()
	if len(rows.anonymous) != 58 {
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
	descriptors, names := maps.Clone(e.state.descriptors), maps.Clone(e.state.names)
	rows, err := e.captureCatalogSourceLocked(context.Background(), catalog, e.roleRead(catalog, e.roles), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	if rows.ref != ref || !maps.Equal(descriptors, e.state.descriptors) || !maps.Equal(names, e.state.names) {
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
	for _, q := range []string{"CREATE DOMAIN opclass_identity_domain AS INT", "CREATE TYPE opclass_identity_enum AS ENUM('a','b')", "CREATE TABLE opclass_identity(n INT,v VARCHAR,nm NAME,d opclass_identity_domain,e opclass_identity_enum)", "INSERT INTO opclass_identity VALUES(1,'one','one',1,'a')"} {
		run(t, e, q)
	}
	for _, tc := range []struct {
		name, key, method string
		oid               OID
	}{
		{"identity_b", "n", "btree", 1978}, {"identity_h", "n pg_catalog.int4_ops", "hash", 10020},
		{"identity_v", "v varchar_ops", "btree", 10044}, {"identity_p", "v varchar_pattern_ops", "hash", 10057},
		{"identity_nm_b", "nm", "btree", 10028}, {"identity_nm_h", "nm", "hash", 10029},
		{"identity_d", "d", "btree", 1978}, {"identity_e", "e", "hash", 10070},
	} {
		run(t, e, "CREATE INDEX "+tc.name+" ON opclass_identity USING "+tc.method+"("+tc.key+")")
		index, root := btreeRoot(t, e, tc.name)
		if tc.method == "hash" {
			if root.hash == nil {
				t.Fatal("missing real hash root", tc)
			}
		} else if root.hash != nil {
			t.Fatal("btree has hash root", tc)
		}
		if got := index.sqlIndex.keys[0].class.OID(); got != uint32(tc.oid) {
			t.Fatal("bound identity", tc, got)
		}
		m, _ := indexMaintenanceMetadata(t, e, e.CatalogRead(), tc.name)
		if !reflect.DeepEqual(m.IndClass, []OID{tc.oid}) {
			t.Fatal("projected index identity", tc, m.IndClass)
		}
		d, ref := pgOpclassUnitSource(t, e)
		rows, err := capturePGOpclass(context.Background(), e.CatalogRead(), ref, d)
		if err != nil {
			t.Fatal(err)
		}
		fact := pgOpclassUnitBag(t, rows, d)[uint32(tc.oid)]
		method := uint32(403)
		if tc.method == "hash" {
			method = 405
		}
		if fact == nil || fact[1] != method {
			t.Fatal("catalog/index method mismatch", tc, fact)
		}
	}
}
