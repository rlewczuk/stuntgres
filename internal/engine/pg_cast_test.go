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

func pgCastUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2605}
	d, ok := e.catalog.state.Relation(a)
	if !ok {
		t.Fatal("missing pg_cast")
	}
	return d, e.catalog.domain.Ref(e.catalog.state.epoch, a)
}

func TestPGCastShapeAndOwnership(t *testing.T) {
	e := New()
	d, _ := pgCastUnitSource(t, e)
	if d.name != "pg_cast" || d.namespace.ObjectID != 11 || d.relationKind != catalogRelation || d.attributeHighWater != 6 || len(d.columns) != 6 {
		t.Fatal(d)
	}
	for i, name := range []string{"oid", "castsource", "casttarget", "castfunc", "castcontext", "castmethod"} {
		id := sqltype.Oid
		if i >= 4 {
			id = sqltype.Char
		}
		c := d.columns[i]
		owned, ok := e.CatalogRead().LookupType(id)
		if !ok || c.Name != name || c.Usage.Type != owned || c.Usage.Type.ID() != id || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 0 || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2605 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[name] != i {
			t.Fatal("descriptor", i, c)
		}
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	foreign, _ := New().CatalogRead().LookupType(sqltype.Oid)
	invalid := *d
	invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
	invalid.columns[0].Usage.Type = foreign
	if err := (relationProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
		t.Fatal("foreign generation accepted")
	}
	if rows, err := capturePGCast(context.Background(), e.CatalogRead(), ids.ObjectRef{}, &invalid); err == nil || rows != nil {
		t.Fatal("foreign source capture", rows, err)
	}
	if len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.acl)
	}
	class := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	rows, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roleRead(e.catalog.read(e.catalog.state), e.roles), ids.ObjectRef{}, class)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows.anonymous {
		oid, _ := row[0].OID()
		if oid != 2605 {
			continue
		}
		name, _ := row[1].Text()
		ns, _ := row[2].OID()
		kind, _ := row[class.index["relkind"]].Text()
		count, _ := row[class.index["relnatts"]].Integer()
		if name != "pg_cast" || ns != 11 || kind != "r" || count != 6 {
			t.Fatal("pg_class projection", row)
		}
		found++
	}
	if found != 1 {
		t.Fatal("pg_class identity count", found)
	}
}

func pgCastUnitBag(t *testing.T, rows *querySource, d *relationDescriptor) map[uint32][]any {
	t.Helper()
	bag, pairs := map[uint32][]any{}, map[[2]uint32]bool{}
	for _, row := range rows.anonymous {
		if len(row) != 6 {
			t.Fatal("row width", row)
		}
		values := make([]any, 6)
		for i, value := range row {
			if value.IsNull() || value.Type() != d.columns[i].Usage.Type {
				t.Fatal("unowned/NULL value", i, value)
			}
			if i < 4 {
				v, err := value.OID()
				if err != nil {
					t.Fatal(err)
				}
				values[i] = v
			} else {
				v, err := value.Text()
				if err != nil {
					t.Fatal(err)
				}
				values[i] = v
			}
		}
		oid := values[0].(uint32)
		pair := [2]uint32{values[1].(uint32), values[2].(uint32)}
		if oid == 0 || bag[oid] != nil || pairs[pair] {
			t.Fatal("duplicate/invalid identity", values)
		}
		bag[oid], pairs[pair] = values, true
	}
	return bag
}

func TestPGCastProjection(t *testing.T) {
	e := New()
	d, ref := pgCastUnitSource(t, e)
	before := maps.Clone(e.catalog.state.descriptors)
	names := maps.Clone(e.catalog.state.names)
	rows, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roleRead(e.catalog.read(e.catalog.state), e.roles), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	if rows.ref != ref {
		t.Fatal("captured reference lost", rows.ref, ref)
	}
	if !maps.Equal(e.catalog.state.descriptors, before) || !maps.Equal(e.catalog.state.names, names) {
		t.Fatal("capture mutated catalog state")
	}
	bag := pgCastUnitBag(t, rows, d)
	// Projection must faithfully carry each owner value, not infer functions
	// or methods. Public oracle tests assert the independent complete bag.
	for i := range sqltype.CastMetadataCount() {
		fact, ok := sqltype.CastMetadataAt(i)
		if !ok {
			t.Fatal(i)
		}
		want := []any{fact.OID, uint32(fact.Source), uint32(fact.Target), fact.Function, string(fact.Context), string(fact.Method)}
		if !reflect.DeepEqual(bag[fact.OID], want) {
			t.Fatal("owner projection", fact, bag[fact.OID], want)
		}
	}
	if len(bag) != sqltype.CastMetadataCount() {
		t.Fatal("projection contains unexpected cast identities")
	}
	// Dedicated capture needs type owners only, not generic relation inventory.
	state := *e.catalog.state
	state.descriptors = maps.Clone(e.catalog.state.descriptors)
	for a := range state.Descriptors() {
		if a.ClassID == ids.RelationClassID {
			state.removeDescriptor(a)
		}
	}
	view := e.catalog.read(&state)
	projected, err := e.captureCatalogSourceLocked(context.Background(), view, e.roleRead(view, e.roles), ref, d)
	if err != nil || !reflect.DeepEqual(pgCastUnitBag(t, projected, d), bag) {
		t.Fatal("unrelated relation enumeration", err)
	}
	state.removeDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int2)})
	if rows, err := capturePGCast(context.Background(), view, ref, d); rows != nil || err == nil {
		t.Fatal("missing endpoint silently skipped", rows, err)
	}
}

type pgCastCancelDuringCapture struct {
	context.Context
	calls int
}

func (c *pgCastCancelDuringCapture) Err() error {
	c.calls++
	if c.calls >= 4 {
		return context.Canceled
	}
	return nil
}

func TestPGCastCancellationAndGenerations(t *testing.T) {
	e := New()
	d, ref := pgCastUnitSource(t, e)
	old, err := capturePGCast(context.Background(), e.CatalogRead(), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	bag := pgCastUnitBag(t, old, d)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{canceled, &pgCastCancelDuringCapture{Context: context.Background()}} {
		rows, err := capturePGCast(ctx, e.CatalogRead(), ref, d)
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled source", rows, err)
		}
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh, freshRef := pgCastUnitSource(t, e)
	newRows, err := capturePGCast(context.Background(), e.CatalogRead(), freshRef, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pgCastUnitBag(t, newRows, fresh), bag) || !reflect.DeepEqual(pgCastUnitBag(t, old, d), bag) {
		t.Fatal("reconstruction changed fixed metadata")
	}
	for i := range d.columns {
		if fresh.columns[i].Usage.Type == d.columns[i].Usage.Type {
			t.Fatal("generation not reminted", i)
		}
		if old.anonymous[0][i].Type() != d.columns[i].Usage.Type || newRows.anonymous[0][i].Type() != fresh.columns[i].Usage.Type {
			t.Fatal("captured generation changed", i)
		}
	}
}
