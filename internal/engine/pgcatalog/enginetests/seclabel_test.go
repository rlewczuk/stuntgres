package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestSeclabelCaptureAuthority(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3596}
	source := view.MustRelation(address)
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.Roles(), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	produce, ok := pgcatalog.ProducerFor(3596)
	if !ok || !pgcatalog.RegisteredSource(view, source) { t.Fatal("missing registered security label producer") }
	if source.RowType != 10092 { t.Fatal("existing row type not attached", source.RowType) }
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10092})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10091})
	if row.Array != 10091 || row.OwningRelation != address || array.OwningRelation != address { t.Fatal("existing row/array identities not attached") }
	if len(source.Columns) != 5 || source.VirtualView || source.CatalogView || !pgcatalog.CatalogHasIndexes(3596) { t.Fatal("catalog descriptor", source) }
	for i, typ := range [...]sqltype.ID{26, 26, 23, 25, 25} {
		owned, ok := view.LookupType(typ)
		column := source.Columns[i]
		if !ok || column.Usage.Type != owned || column.OriginTable != 3596 || column.OriginAttribute != int16(i+1) || column.Nullable || !column.NullabilityKnown { t.Fatal("owned descriptor", i, column) }
		fact := source.ColumnMeta.CatalogAttributes[i]
		collation := uint32(0)
		if i >= 3 { collation = 950 }
		if !fact.NotNull || fact.Dimensions != 0 || fact.Collation != collation || column.Usage != owned.Usage() {
			t.Fatal("bootstrap attribute facts and result usage have separate authority", i, fact, column.Usage)
		}
	}
	state := view.State()
	for range 2 {
		if rows, err := produce(in); err != nil || len(rows) != 0 { t.Fatal("truthful empty source", rows, err) }
		if got, err := core.CaptureCatalogSourceRows(in, produce); err != nil || got == nil || got.Ref != in.Ref || len(got.Anonymous) != 0 { t.Fatal("empty source capture", got, err) }
	}
	if e.CatalogRead().State() != state { t.Fatal("capture published state") }
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(x *pgcatalog.Input) { x.Owner = nil },
		func(x *pgcatalog.Input) { x.Owner = foreign.CatalogRead().Owner() },
		func(x *pgcatalog.Input) { x.Catalog = foreign.CatalogRead() },
		func(x *pgcatalog.Input) { x.Roles = foreign.Roles() },
		func(x *pgcatalog.Input) { x.Source = nil },
		func(x *pgcatalog.Input) { x.Ref = ids.ObjectRef{} },
		func(x *pgcatalog.Input) { x.Ref.Epoch++ },
		func(x *pgcatalog.Input) { x.Ref.Address.ObjectID++ },
		func(x *pgcatalog.Input) { x.Ref = foreign.Catalog.Domain.Ref(x.Catalog.Epoch(), address) },
		func(x *pgcatalog.Input) { copy := *x.Source; x.Source = &copy },
	} {
		x := in
		mutate(&x)
		if rows, err := produce(x); rows != nil || err == nil { t.Fatal("foreign authority admitted", rows, err) }
	}
	// Publish forged descriptors into a matching captured snapshot, rather
	// than accidentally rejecting every malformed shape solely by pointer.
	for _, mutate := range []func(*catalog.Relation){
		func(d *catalog.Relation) { d.Columns = d.Columns[:4] },
		func(d *catalog.Relation) { d.Columns[0].Name = "forged" },
		func(d *catalog.Relation) { d.Columns[0].OriginTable++ },
		func(d *catalog.Relation) { d.Columns[0].OriginAttribute++ },
		func(d *catalog.Relation) { d.Columns[0].Nullable = true },
		func(d *catalog.Relation) { d.Columns[0].NullabilityKnown = false },
		func(d *catalog.Relation) { d.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(26) },
		func(d *catalog.Relation) { d.Columns[0].Usage.Typmod = 1 },
		func(d *catalog.Relation) { d.Columns[0].DeclaredDimensions = 1 },
		func(d *catalog.Relation) { d.ColumnMeta.CatalogAttributes[3].Collation = 100 },
		func(d *catalog.Relation) { d.ColumnMeta.CatalogAttributes[0].NotNull = false },
		func(d *catalog.Relation) { d.ColumnMeta.CatalogAttributes[0].Dimensions = 1 },
	} {
		d := *source
		d.Columns = append([]catalog.Column(nil), source.Columns...)
		d.ColumnMeta.CatalogAttributes = append([]catalog.CatalogAttribute(nil), source.ColumnMeta.CatalogAttributes...)
		mutate(&d)
		s := catalog.CloneState(state)
		s.SetDescriptor(address, &d)
		x := in
		x.Catalog, x.Source = view.WithState(s), &d
		if rows, err := produce(x); rows != nil || err == nil { t.Fatal("forged shape admitted", rows, err) }
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	x := in
	x.Context, x.Source = ctx, nil
	if rows, err := produce(x); rows != nil || !errors.Is(err, context.Canceled) { t.Fatal("cancellation precedence", rows, err) }
	if got, err := core.CaptureCatalogSourceRows(x, produce); got != nil || !errors.Is(err, context.Canceled) { t.Fatal("canceled capture published", got, err) }
}
