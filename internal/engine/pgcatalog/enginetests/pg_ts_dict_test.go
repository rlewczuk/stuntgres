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
)

func TestPGTSDictCaptureContract(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3600}
	source := view.MustRelation(address)
	ref := e.Catalog.Domain.Ref(view.Epoch(), address)
	produce := registeredCatalogProducer(3600)
	if source.Name() != "pg_ts_dict" || source.Namespace().ObjectID != 11 || len(source.Columns) != 6 {
		t.Fatal("dictionary source identity", source)
	}
	names := []string{"oid", "dictname", "dictnamespace", "dictowner", "dicttemplate", "dictinitoption"}
	types := []uint32{26, 19, 26, 26, 26, 25}
	for i, column := range source.Columns {
		typ, ok := view.LookupType(column.Usage.Type.ID())
		if !ok || column.Usage.Type != typ || uint32(typ.ID()) != types[i] || column.Name != names[i] || column.OriginTable != 3600 || column.OriginAttribute != int16(i+1) || !column.NullabilityKnown || column.Nullable != (i == 5) || column.Usage.Typmod != -1 || source.ColumnMeta.CatalogAttributes[i].NotNull != (i != 5) {
			t.Fatal("dictionary source-owned column contract", i, column)
		}
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: source, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}
	nextOID, nextRevision := e.Catalog.NextOID(), e.Catalog.NextRevision()
	for range 2 {
		got, err := core.CaptureCatalogSourceRows(input, produce)
		if err != nil || got == nil {
			t.Fatal("seed dictionary capture", got, err)
		}
		if got.Ref != ref || got.Anonymous == nil || len(got.Anonymous) != 1 {
			t.Fatal("seed dictionary source must contain simple", got)
		}
		name, nameErr := got.Anonymous[0][1].Text()
		if nameErr != nil || name != "simple" || !got.Anonymous[0][5].IsNull() {
			t.Fatal("simple dictionary seed name/options", name, got.Anonymous[0][5])
		}
	}
	empty := catalog.CloneState(view.State())
	empty.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765})
	emptyInput := input
	emptyInput.Catalog = e.Catalog.Read(empty)
	got, err := core.CaptureCatalogSourceRows(emptyInput, produce)
	if err != nil || got == nil || got.Ref != ref || got.Anonymous == nil || len(got.Anonymous) != 0 {
		t.Fatal("empty dictionary source must be nonnil", got, err)
	}
	if e.Catalog.State != view.State() || e.Catalog.NextOID() != nextOID || e.Catalog.NextRevision() != nextRevision {
		t.Fatal("dictionary read changed catalog/frontiers")
	}
	ordinaryFailure := func(in pgcatalog.Input, canceled bool) {
		t.Helper()
		got, err := core.CaptureCatalogSourceRows(in, produce)
		if got != nil || err == nil {
			t.Fatal("invalid dictionary capture published", got, err)
		}
		if canceled && !errors.Is(err, context.Canceled) {
			t.Fatal("lost cancellation", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary capture error acquired SQLSTATE", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bad := input
	bad.Context = ctx
	ordinaryFailure(bad, true)
	foreign := engine.New().CatalogRead()
	stale := ref
	stale.Epoch++
	wrongAddress := ref
	wrongAddress.Address.ObjectID = 3764
	for _, owner := range []*catalog.Owner{nil, foreign.Owner()} {
		bad := input
		bad.Owner = owner
		ordinaryFailure(bad, false)
	}
	for _, tc := range []struct {
		view   catalog.Read
		ref    ids.ObjectRef
		source *catalog.Relation
	}{
		{catalog.Read{}, ref, source}, {foreign, ref, source}, {view, stale, source}, {view, wrongAddress, source}, {view, ref, nil},
	} {
		bad := input
		bad.Catalog, bad.Owner, bad.Ref, bad.Source = tc.view, tc.view.Owner(), tc.ref, tc.source
		ordinaryFailure(bad, false)
	}
	for i := range source.Columns {
		state := catalog.CloneState(view.State())
		badSource := *source
		badSource.Columns = append([]catalog.Column(nil), source.Columns...)
		badSource.Columns[i].Usage.Type, _ = foreign.LookupType(source.Columns[i].Usage.Type.ID())
		state.SetDescriptor(address, &badSource)
		bad := input
		bad.Catalog = e.Catalog.Read(state)
		bad.Owner, bad.Source = bad.Catalog.Owner(), &badSource
		ordinaryFailure(bad, false)
	}
	for _, malformed := range []string{"width", "name"} {
		state := catalog.CloneState(view.State())
		badSource := *source
		badSource.Columns = append([]catalog.Column(nil), source.Columns...)
		if malformed == "width" {
			badSource.Columns = badSource.Columns[:5]
		} else {
			badSource.Columns[0].Name = "not_oid"
		}
		state.SetDescriptor(address, &badSource)
		bad := input
		bad.Catalog = e.Catalog.Read(state)
		bad.Owner, bad.Source = bad.Catalog.Owner(), &badSource
		ordinaryFailure(bad, false)
	}
}
