package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

var opfamilyUnitNames = []string{"oid", "opfmethod", "opfname", "opfnamespace", "opfowner"}

func opfamilyUnitCapture(view catalog.Read, d *catalog.Relation, ref ids.ObjectRef, ctx context.Context) (*exec.Source, error) {
	return core.CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: view, Ref: ref, Source: d, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}, registeredCatalogProducer(2753))
}

func opfamilyUnitBag(t *testing.T, rows *exec.Source, d *catalog.Relation) map[uint32][]any {
	t.Helper()
	bag := map[uint32][]any{}
	for _, row := range rows.Anonymous {
		if len(row) != 5 || cap(row) != 5 {
			t.Fatal("detached exact-width backing", len(row), cap(row))
		}
		values := make([]any, 5)
		for i, v := range row {
			if v.IsNull() || v.Type() != d.Columns[i].Usage.Type {
				t.Fatal("foreign/null family cell", i)
			}
			var err error
			if i == 2 {
				values[i], err = v.Text()
			} else {
				values[i], err = v.OID()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		oid := values[0].(uint32)
		if oid == 0 || bag[oid] != nil {
			t.Fatal("duplicate/zero family identity", oid)
		}
		bag[oid] = values
	}
	return bag
}

func opfamilyUnitFacts(t *testing.T, bag map[uint32][]any) {
	t.Helper()
	for _, want := range [][]any{
		{uint32(1976), uint32(403), "integer_ops", uint32(11), uint32(10)},
		{uint32(1977), uint32(405), "integer_ops", uint32(11), uint32(10)},
		{uint32(1994), uint32(403), "text_ops", uint32(11), uint32(10)},
	} {
		if !reflect.DeepEqual(bag[want[0].(uint32)], want) {
			t.Fatal("released family witness", want, bag[want[0].(uint32)])
		}
	}
}

func TestPGOpfamilyCaptureOwnership(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	d, ref := amopUnitSource(t, e, 2753)
	if d.Name() != "pg_opfamily" || d.Namespace().ObjectID != 11 || d.RelationKind != catalog.CatalogRelation || len(d.Columns) != 5 || d.ColumnMeta.HighWater != 5 {
		t.Fatal("family descriptor", d)
	}
	for i, name := range opfamilyUnitNames {
		id := sqltype.Oid
		if i == 2 {
			id = sqltype.Name
		}
		typ, ok := view.LookupType(id)
		c := d.Columns[i]
		if !ok || c.Name != name || c.Usage.Type != typ || c.Usage.Typmod != -1 || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2753 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[name] != i {
			t.Fatal("owned family column", i, c)
		}
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("family PUBLIC SELECT ACL")
	}
	if err := core.RelationProvider.Validate(view, d); err != nil {
		t.Fatal(err)
	}
	descriptors, names := maps.Collect(view.Descriptors()), maps.Collect(view.Names())
	rows, err := opfamilyUnitCapture(view, d, ref, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bag := opfamilyUnitBag(t, rows, d)
	opfamilyUnitFacts(t, bag)
	if rows.Ref != ref {
		t.Fatal("capture reference", rows.Ref, ref)
	}
	owners := 0
	for _, descriptor := range view.Descriptors() {
		family, ok := descriptor.(*catalog.OperatorFamily)
		if !ok {
			continue
		}
		owners++
		want := []any{uint32(family.Address().ObjectID), uint32(family.Method), family.Name(), uint32(family.Namespace().ObjectID), uint32(family.Owner.ID())}
		if !reflect.DeepEqual(bag[uint32(family.Address().ObjectID)], want) {
			t.Fatal("immutable owner projection", family, want)
		}
	}
	if owners != len(bag) {
		t.Fatal("invented family rows", owners, len(bag))
	}
	again, err := opfamilyUnitCapture(view, d, ref, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if &again.Anonymous[0][0] == &rows.Anonymous[0][0] || !reflect.DeepEqual(opfamilyUnitBag(t, again, d), bag) {
		t.Fatal("capture backing reused or facts changed")
	}
	if !maps.Equal(descriptors, maps.Collect(view.Descriptors())) || !maps.Equal(names, maps.Collect(view.Names())) {
		t.Fatal("capture mutated catalog")
	}
	backing := &rows.Anonymous[0][0]
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	fresh, freshRef := amopUnitSource(t, e, 2753)
	freshRows, err := opfamilyUnitCapture(e.CatalogRead(), fresh, freshRef, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range d.Columns {
		if fresh.Columns[i].Usage.Type == d.Columns[i].Usage.Type {
			t.Fatal("old generation type reused", i)
		}
	}
	if &rows.Anonymous[0][0] != backing || !reflect.DeepEqual(opfamilyUnitBag(t, rows, d), bag) || !reflect.DeepEqual(opfamilyUnitBag(t, freshRows, fresh), bag) {
		t.Fatal("reset changed retained family backing/facts")
	}
	opfamilyUnitFacts(t, opfamilyUnitBag(t, freshRows, fresh))
	if invalid, err := opfamilyUnitCapture(e.CatalogRead(), d, freshRef, context.Background()); invalid != nil || err == nil {
		t.Fatal("stale source accepted", invalid, err)
	}
}

func TestPGOpfamilyCaptureCancellation(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	d, ref := amopUnitSource(t, e, 2753)
	descriptors, names := maps.Collect(view.Descriptors()), maps.Collect(view.Names())
	pre, cancel := context.WithCancel(context.Background())
	cancel()
	during, stop := context.WithCancel(context.Background())
	defer stop()
	for _, ctx := range []context.Context{pre, &cancelAtErrCheck{Context: during, cancel: stop, at: 5}} {
		partial, err := opfamilyUnitCapture(view, d, ref, ctx)
		if partial != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled partial family publication", partial, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("context error gained SQLSTATE", err)
		}
	}
	if !maps.Equal(descriptors, maps.Collect(view.Descriptors())) || !maps.Equal(names, maps.Collect(view.Names())) {
		t.Fatal("canceled capture mutated catalog")
	}
	rows, err := opfamilyUnitCapture(view, d, ref, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	opfamilyUnitFacts(t, opfamilyUnitBag(t, rows, d))
}
