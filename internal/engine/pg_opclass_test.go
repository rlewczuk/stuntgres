package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgOpclassUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2616}
	d, ok := e.catalog.state.Relation(a)
	if !ok {
		t.Fatal("missing pg_opclass")
	}
	return d, e.catalog.Domain.Ref(e.catalog.state.Epoch(), a)
}

func pgOpclassUnitBag(t *testing.T, rows *querySource, d *catalog.Relation) map[uint32][]any {
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
			if v.IsNull() || v.Type() != d.Columns[i].Usage.Type {
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
	view := e.CatalogRead()
	d, ref := pgOpclassUnitSource(t, e)
	if d.Name() != "pg_opclass" || d.Namespace().ObjectID != 11 || d.RowType != 10013 || !d.History.HasIndexes || d.RelationKind != catalog.CatalogRelation || d.ColumnMeta.HighWater != 9 || len(d.Columns) != 9 {
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
		owned, ok := view.LookupType(id)
		c := d.Columns[i]
		if !ok || c.Name != name || c.Usage.Type != owned || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2616 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[name] != i {
			t.Fatal("column", i, c)
		}
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC read", d.Security.ACL)
	}
	if err := (relationProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	descriptors, names := maps.Collect(e.catalog.state.Descriptors()), maps.Collect(e.catalog.state.Names())
	rows, err := e.captureCatalogSourceLocked(context.Background(), view, e.roles.read(e.catalog.owner(), view, e.roles.published), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	if rows.ref != ref || !maps.Equal(descriptors, maps.Collect(e.catalog.state.Descriptors())) || !maps.Equal(names, maps.Collect(e.catalog.state.Names())) {
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
	invalid.Columns = append([]catalog.Column(nil), d.Columns...)
	invalid.Columns[0].Usage.Type = foreign
	for _, source := range []*catalog.Relation{&invalid, {Columns: d.Columns[:8]}} {
		if rows, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: view, Ref: ref, Source: source, Owner: (view).Owner(), Runtime: engineCatalogRuntime{}}, capturePGOpclass); rows != nil || err == nil {
			t.Fatal("invalid source accepted", rows, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	during, cancelDuring := context.WithCancel(context.Background())
	defer cancelDuring()
	for _, ctx := range []context.Context{ctx, &cancelAtErrCheck{Context: during, cancel: cancelDuring, at: 5}} {
		if rows, err := captureCatalogSourceRows(catalogSourceInput{Context: ctx, Catalog: view, Ref: ref, Source: d, Owner: (view).Owner(), Runtime: engineCatalogRuntime{}}, capturePGOpclass); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled capture", rows, err)
		}
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh, freshRef := pgOpclassUnitSource(t, e)
	freshRows, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: e.CatalogRead(), Ref: freshRef, Source: fresh, Owner: (e.CatalogRead()).Owner(), Runtime: engineCatalogRuntime{}}, capturePGOpclass)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Columns[0].Usage.Type == d.Columns[0].Usage.Type {
		t.Fatal("generation not reowned")
	}
	if !reflect.DeepEqual(pgOpclassUnitBag(t, freshRows, fresh), bag) || !reflect.DeepEqual(pgOpclassUnitBag(t, rows, d), bag) {
		t.Fatal("replacement changed immutable facts/old capture")
	}
	if rows, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: e.CatalogRead(), Ref: freshRef, Source: d, Owner: (e.CatalogRead()).Owner(), Runtime: engineCatalogRuntime{}}, capturePGOpclass); rows != nil || err == nil {
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
		method       catalog.IndexMethod
		oid          ids.OID
	}{
		{sqltype.Int4, "int4_ops", "1", catalog.BtreeMethod, 1978},
		{sqltype.Int4, "int4_ops", "1", catalog.HashMethod, 10020},
		{sqltype.VarChar, "varchar_ops", "one", catalog.BtreeMethod, 10044},
		{sqltype.VarChar, "varchar_pattern_ops", "one", catalog.HashMethod, 10057},
		{sqltype.Name, "name_ops", "one", catalog.BtreeMethod, 10028},
		{sqltype.Name, "name_ops", "one", catalog.HashMethod, 10029},
		{48000, "int4_ops", "1", catalog.BtreeMethod, 1978},
		{48001, "enum_ops", "a", catalog.HashMethod, 10070},
	} {
		typ, _ := e.CatalogRead().LookupType(tc.id)
		if tc.id == 48000 {
			typ = domain
		}
		if tc.id == 48001 {
			typ = enum
		}
		class, ok := tc.method.LookupClass(tc.class)
		if !ok || class.OID() != uint32(tc.oid) {
			t.Fatal("class identity", tc, class)
		}
		parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ids.RelationClassID, ids.OID(48100 + i*2), 0}, ObjectNamespace: ids.ObjectAddress{ids.NamespaceClassID, ids.PublicNamespaceOID, 0}, ObjectName: "identity_parent", RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "k", Usage: typ.Usage()}}, Index: map[string]int{"k": 0}}
		index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ids.RelationClassID, ids.OID(48101 + i*2), 0}, ObjectNamespace: parent.Namespace(), ObjectName: "identity_index", RelationKind: catalog.IndexRelation, Columns: parent.Columns, Attributes: []int32{1}, SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Method: tc.method, Keys: []catalog.IndexKey{{Attribute: 1, Class: class, ExplicitClass: true}}, Ready: true, Valid: true, Live: true}}
		e.catalog.state.SetDescriptor(parent.Address(), parent)
		e.catalog.state.SetDescriptor(index.Address(), index)
		value, err := typ.Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		root, err := buildIndexRoot(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, parent, index.SQLIndex, &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value}}}}, nil)
		if err != nil || root == nil || (root.Hash != nil) != (tc.method == catalog.HashMethod) {
			t.Fatal("real root", tc, root, err)
		}
		view := e.CatalogRead()
		m, _, err := projectIndexMetadata(context.Background(), view, e.catalog.Domain.Ref(view.Epoch(), index.Address()))
		if err != nil || !reflect.DeepEqual(m.IndClass, []ids.OID{tc.oid}) {
			t.Fatal("projected index identity", tc, m.IndClass, err)
		}
		d, ref := pgOpclassUnitSource(t, e)
		rows, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: view, Ref: ref, Source: d, Owner: (view).Owner(), Runtime: engineCatalogRuntime{}}, capturePGOpclass)
		if err != nil {
			t.Fatal(err)
		}
		method := uint32(403)
		if tc.method == catalog.HashMethod {
			method = 405
		}
		if fact := pgOpclassUnitBag(t, rows, d)[uint32(tc.oid)]; fact == nil || fact[1] != method {
			t.Fatal("catalog/index method mismatch", tc, fact)
		}
	}
}
