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

func pgCastUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2605}
	d, ok := e.Catalog.State.Relation(a)
	if !ok {
		t.Fatal("missing pg_cast")
	}
	return d, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), a)
}

func TestPGCastShapeAndOwnership(t *testing.T) {
	e := New()
	d, _ := pgCastUnitSource(t, e)
	if d.Name() != "pg_cast" || d.Namespace().ObjectID != 11 || d.RelationKind != catalog.CatalogRelation || d.ColumnMeta.HighWater != 6 || len(d.Columns) != 6 {
		t.Fatal(d)
	}
	for i, name := range []string{"oid", "castsource", "casttarget", "castfunc", "castcontext", "castmethod"} {
		id := sqltype.Oid
		if i >= 4 {
			id = sqltype.Char
		}
		c := d.Columns[i]
		owned, ok := e.CatalogRead().LookupType(id)
		if !ok || c.Name != name || c.Usage.Type != owned || c.Usage.Type.ID() != id || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 0 || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2605 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[name] != i {
			t.Fatal("descriptor", i, c)
		}
	}
	if err := (RelationProvider).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	foreign, _ := New().CatalogRead().LookupType(sqltype.Oid)
	invalid := *d
	invalid.Columns = append([]catalog.Column(nil), d.Columns...)
	invalid.Columns[0].Usage.Type = foreign
	if err := (RelationProvider).Validate(e.CatalogRead(), &invalid); err == nil {
		t.Fatal("foreign generation accepted")
	}
	if rows, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ids.ObjectRef{}, Source: &invalid, Owner: (e.CatalogRead()).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGCast); err == nil || rows != nil {
		t.Fatal("foreign source capture", rows, err)
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.Security.ACL)
	}
	class := e.Catalog.State.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	rows, err := captureCatalogSourceLocked(e, context.Background(), e.CatalogRead(), e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), ids.ObjectRef{}, class)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows.Anonymous {
		oid, _ := row[0].OID()
		if oid != 2605 {
			continue
		}
		name, _ := row[1].Text()
		ns, _ := row[2].OID()
		kind, _ := row[class.Index["relkind"]].Text()
		count, _ := row[class.Index["relnatts"]].Integer()
		if name != "pg_cast" || ns != 11 || kind != "r" || count != 6 {
			t.Fatal("pg_class projection", row)
		}
		found++
	}
	if found != 1 {
		t.Fatal("pg_class identity count", found)
	}
}

func pgCastUnitBag(t *testing.T, rows *exec.Source, d *catalog.Relation) map[uint32][]any {
	t.Helper()
	bag, pairs := map[uint32][]any{}, map[[2]uint32]bool{}
	for _, row := range rows.Anonymous {
		if len(row) != 6 {
			t.Fatal("row width", row)
		}
		values := make([]any, 6)
		for i, value := range row {
			if value.IsNull() || value.Type() != d.Columns[i].Usage.Type {
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
	before := maps.Collect(e.Catalog.State.Descriptors())
	names := maps.Collect(e.Catalog.State.Names())
	rows, err := captureCatalogSourceLocked(e, context.Background(), e.CatalogRead(), e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Ref != ref {
		t.Fatal("captured reference lost", rows.Ref, ref)
	}
	if !maps.Equal(maps.Collect(e.Catalog.State.Descriptors()), before) || !maps.Equal(maps.Collect(e.Catalog.State.Names()), names) {
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
	state := *e.Catalog.State
	for a := range state.Descriptors() {
		if a.ClassID == ids.RelationClassID {
			state.RemoveDescriptor(a)
		}
	}
	view := e.Catalog.Read(&state)
	projected, err := captureCatalogSourceLocked(e, context.Background(), view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ref, d)
	if err != nil || !reflect.DeepEqual(pgCastUnitBag(t, projected, d), bag) {
		t.Fatal("unrelated relation enumeration", err)
	}
	state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int2)})
	if rows, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: d, Owner: (view).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGCast); rows != nil || err == nil {
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
	old, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: d, Owner: (e.CatalogRead()).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGCast)
	if err != nil {
		t.Fatal(err)
	}
	bag := pgCastUnitBag(t, old, d)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{canceled, &pgCastCancelDuringCapture{Context: context.Background()}} {
		rows, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: e.CatalogRead(), Ref: ref, Source: d, Owner: (e.CatalogRead()).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGCast)
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled source", rows, err)
		}
	}
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	fresh, freshRef := pgCastUnitSource(t, e)
	newRows, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: freshRef, Source: fresh, Owner: (e.CatalogRead()).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGCast)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pgCastUnitBag(t, newRows, fresh), bag) || !reflect.DeepEqual(pgCastUnitBag(t, old, d), bag) {
		t.Fatal("reconstruction changed fixed metadata")
	}
	for i := range d.Columns {
		if fresh.Columns[i].Usage.Type == d.Columns[i].Usage.Type {
			t.Fatal("generation not reminted", i)
		}
		if old.Anonymous[0][i].Type() != d.Columns[i].Usage.Type || newRows.Anonymous[0][i].Type() != fresh.Columns[i].Usage.Type {
			t.Fatal("captured generation changed", i)
		}
	}
}
