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
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatRecoveryPrefetchProjection(t *testing.T) {
	// The fixed inventoried OID and existing registry API keep this test-first
	// definition compilable before the view producer is registered.
	const relationID ids.OID = 12244
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: relationID}
	source, ok := view.Relation(address)
	if !ok {
		t.Fatal("missing registered recovery-prefetch source")
	}
	produce, ok := pgcatalog.ProducerFor(relationID)
	if !ok {
		t.Fatal("missing recovery-prefetch producer")
	}
	input := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	before := e.Statistics.ReadShared(statistics.RecoveryPrefetch)
	checkMissing := func(rows [][]sqltype.Value) {
		t.Helper()
		if len(rows) != 1 || len(rows[0]) != 10 {
			t.Fatal("one ten-cell row", rows)
		}
		for i, cell := range rows[0] {
			if cell.Type() != source.Columns[i].Usage.Type {
				t.Fatal("projection lost captured descriptor ownership", i, cell.Type(), source.Columns[i].Usage.Type)
			}
			id := sqltype.Int8
			if i == 0 {
				id = sqltype.Timestamptz
			} else if i >= 7 {
				id = sqltype.Int4
			}
			if cell.Type().ID() != id {
				t.Fatal("exact projection type", i, cell.Type().ID(), id)
			}
			if i == 0 {
				if !cell.IsNull() {
					t.Fatal("missing detached reset metadata was invented", cell)
				}
			} else if n, err := cell.Integer(); err != nil || n != 0 {
				t.Fatal("missing facility must be exact typed zero", i, n, err)
			}
		}
	}
	rows, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	checkMissing(rows)
	rows[0][0] = sqltype.Null(source.Columns[1].Usage.Type)
	rows[0] = nil
	rows, err = produce(input)
	if err != nil {
		t.Fatal(err)
	}
	checkMissing(rows)
	captured, err := catalogrt.CaptureRows(input, produce)
	if err != nil || captured == nil || captured.Ref != input.Ref {
		t.Fatal("common capture identity", captured, err)
	}
	checkMissing(captured.Anonymous)
	foreign := engine.New()
	reject := func(in pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(in); err == nil || rows != nil {
			t.Fatal("invalid projection authority published rows", rows, err)
		}
		if rows, err := catalogrt.CaptureRows(in, produce); err == nil || rows != nil {
			t.Fatal("invalid source capture published rows", rows, err)
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
		func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Domain.Ref(in.Catalog.Epoch(), address) },
		func(in *pgcatalog.Input) { copied := *in.Source; in.Source = &copied },
	} {
		in := input
		change(&in)
		reject(in)
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.ObjectName = "forged_prefetch" },
		func(r *catalog.Relation) { r.VirtualView = false },
		func(r *catalog.Relation) { r.CatalogView = false },
		func(r *catalog.Relation) { r.Columns = r.Columns[:9] },
		func(r *catalog.Relation) { r.Columns[7], r.Columns[8] = r.Columns[8], r.Columns[7] },
		func(r *catalog.Relation) { r.Columns[7].Usage.Type, _ = view.LookupType(sqltype.Int8) },
		func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Timestamptz) },
		func(r *catalog.Relation) { r.Columns[0].Usage.Typmod = 0 },
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
		t.Fatal("pre-cancellation must publish no prefix", rows, err)
	}
	if rows, err := catalogrt.CaptureRows(input, produce); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled common capture", rows, err)
	}
	if got := e.Statistics.ReadShared(statistics.RecoveryPrefetch); got != before {
		t.Fatal("pure projection changed live provider", got, before)
	}
}
