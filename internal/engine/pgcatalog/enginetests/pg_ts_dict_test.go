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
	stemAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "english_stem"})
	stem, ok := view.TSDictionary(stemAddress)
	if !ok || stemAddress.ObjectID == 0 || stemAddress.ObjectID == 3765 {
		t.Fatal("actual English dictionary seed", stemAddress)
	}
	arabicAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "arabic_stem"})
	arabic, ok := view.TSDictionary(arabicAddress)
	if !ok || arabicAddress.ObjectID == 0 || arabicAddress == stemAddress || arabicAddress.ObjectID == 3765 || arabic.Template != stem.Template {
		t.Fatal("actual Arabic dictionary seed", arabicAddress)
	}
	for range 2 {
		got, err := core.CaptureCatalogSourceRows(input, produce)
		if err != nil || got == nil {
			t.Fatal("seed dictionary capture", got, err)
		}
		if got.Ref != ref || got.Anonymous == nil || len(got.Anonymous) != 3 {
			t.Fatal("exact simple/English/Arabic dictionary source inventory", got)
		}
		seen := map[string]bool{}
		for _, row := range got.Anonymous {
			if len(row) != 6 {
				t.Fatal("dictionary row width", row)
			}
			for i, value := range row {
				if value.Type() != source.Columns[i].Usage.Type || value.IsNull() && i != 5 {
					t.Fatal("source-owned dictionary cell", i, value)
				}
			}
			name, err := row[1].Text()
			if err != nil || seen[name] || name != "simple" && name != "english_stem" && name != "arabic_stem" {
				t.Fatal("dictionary seed name", name, err)
			}
			seen[name] = true
			oid, template := uint32(3765), uint32(3727)
			if name == "simple" {
				if !row[5].IsNull() {
					t.Fatal("simple dictionary seed options", row[5])
				}
			} else if name == "english_stem" {
				oid, template = uint32(stemAddress.ObjectID), uint32(stem.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'english', stopwords = 'english'" {
					t.Fatal("English seed ordered options", options, err)
				}
			} else {
				oid, template = uint32(arabicAddress.ObjectID), uint32(arabic.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'arabic'" {
					t.Fatal("Arabic seed language-only options", options, err)
				}
			}
			for i, want := range map[int]uint32{0: oid, 2: 11, 3: 10, 4: template} {
				value, err := row[i].OID()
				if err != nil || value != want {
					t.Fatal("dictionary seed relationship", name, i, value, want, err)
				}
			}
		}
	}
	empty := catalog.CloneState(view.State())
	empty.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765})
	empty.RemoveDescriptor(stemAddress)
	empty.RemoveDescriptor(arabicAddress)
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
