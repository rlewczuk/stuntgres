package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGStatReplicationSlotsSource(t *testing.T) {
	e, foreign := engine.New(), engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatReplicationSlotsOID}
	source, ok := view.Relation(address)
	if !ok {
		t.Fatal("missing registered replication slots source")
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatReplicationSlotsOID)
	if !ok {
		t.Fatal("missing replication slots producer")
	}
	input := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	for range 2 {
		rows, err := produce(input)
		if err != nil || rows != nil {
			t.Fatal("no inventory must allocate no rows", rows, err)
		}
		captured, err := catalogrt.CaptureRows(input, produce)
		if err != nil || captured == nil || captured.Ref != input.Ref || len(captured.Anonymous) != 0 {
			t.Fatal("typed empty common source capture", captured, err)
		}
	}
	reject := func(in pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(in); err == nil || rows != nil {
			t.Fatal("invalid authority published rows", rows, err)
		}
		if captured, err := catalogrt.CaptureRows(in, produce); err == nil || captured != nil {
			t.Fatal("invalid common capture published source", captured, err)
		}
	}
	for _, change := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Source = nil },
		func(in *pgcatalog.Input) { in.Owner = nil },
		func(in *pgcatalog.Input) { in.Owner = foreign.CatalogRead().Owner() },
		func(in *pgcatalog.Input) { in.Catalog = catalog.Read{} },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Ref = ids.ObjectRef{} },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.ObjectID++ },
		func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Domain.Ref(view.Epoch(), address) },
		func(in *pgcatalog.Input) { copied := *source; in.Source = &copied },
		func(in *pgcatalog.Input) { in.Roles = authorization.Read{} },
		func(in *pgcatalog.Input) { in.Roles = foreign.RoleStore.Published.Root.Read(authorization.PrincipalRef{}) },
	} {
		in := input
		change(&in)
		reject(in)
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.ObjectName = "forged_slots" },
		func(r *catalog.Relation) { r.VirtualView = false },
		func(r *catalog.Relation) { r.CatalogView = false },
		func(r *catalog.Relation) { r.Columns = r.Columns[:9] },
		func(r *catalog.Relation) { r.Columns[1], r.Columns[2] = r.Columns[2], r.Columns[1] },
		func(r *catalog.Relation) { r.Columns[1].Usage.Type, _ = view.LookupType(sqltype.Int4) },
		func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Text) },
		func(r *catalog.Relation) { r.Columns[0].Usage.Typmod = 0 },
		func(r *catalog.Relation) { r.Columns[0].Usage.Collation.ID = 0 },
		func(r *catalog.Relation) { r.Columns[1].Usage.Collation.ID = 950 },
		func(r *catalog.Relation) { r.Columns[1].Nullable = false },
		func(r *catalog.Relation) { r.Columns[1].NullabilityKnown = false },
		func(r *catalog.Relation) { r.Columns[1].DeclaredDimensions = 1 },
	} {
		state := *view.State()
		copied := *source
		copied.Columns = append([]catalog.Column(nil), source.Columns...)
		change(&copied)
		state.SetDescriptor(address, &copied)
		in := input
		in.Catalog, in.Source = e.Catalog.Read(&state), &copied
		reject(in)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	input.Context, input.Source = ctx, nil
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation must remain ordinary and publish no prefix", rows, err)
	}
	if rows, err := catalogrt.CaptureRows(input, produce); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled common capture", rows, err)
	}
}
