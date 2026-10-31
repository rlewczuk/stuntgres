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
	armenianAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "armenian_stem"})
	armenian, ok := view.TSDictionary(armenianAddress)
	if !ok || armenianAddress.ObjectID == 0 || armenianAddress == stemAddress || armenianAddress == arabicAddress || armenianAddress.ObjectID == 3765 || armenian.Template != stem.Template {
		t.Fatal("actual Armenian dictionary seed", armenianAddress)
	}
	basqueAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "basque_stem"})
	basque, ok := view.TSDictionary(basqueAddress)
	if !ok || basqueAddress.ObjectID == 0 || basqueAddress == stemAddress || basqueAddress == arabicAddress || basqueAddress == armenianAddress || basqueAddress.ObjectID == 3765 || basque.Template != stem.Template {
		t.Fatal("actual distinct Basque dictionary seed", basqueAddress)
	}
	danishAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "danish_stem"})
	danish, ok := view.TSDictionary(danishAddress)
	if !ok || danishAddress.ObjectID == 0 || danishAddress == stemAddress || danishAddress == arabicAddress || danishAddress == armenianAddress || danishAddress == basqueAddress || danishAddress.ObjectID == 3765 || danish.Template != stem.Template {
		t.Fatal("actual distinct Danish dictionary seed", danishAddress)
	}
	dutchAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "dutch_stem"})
	dutch, ok := view.TSDictionary(dutchAddress)
	if !ok || dutchAddress.ObjectID == 0 || dutchAddress == stemAddress || dutchAddress == arabicAddress || dutchAddress == armenianAddress || dutchAddress == basqueAddress || dutchAddress == danishAddress || dutchAddress.ObjectID == 3765 || dutch.Template != stem.Template {
		t.Fatal("actual distinct Dutch dictionary seed", dutchAddress)
	}
	estonianAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "estonian_stem"})
	estonian, ok := view.TSDictionary(estonianAddress)
	if !ok || estonianAddress.ObjectID == 0 || estonianAddress == stemAddress || estonianAddress == arabicAddress || estonianAddress == armenianAddress || estonianAddress == basqueAddress || estonianAddress == danishAddress || estonianAddress == dutchAddress || estonianAddress.ObjectID == 3765 || estonian.Template != stem.Template {
		t.Fatal("actual distinct Estonian dictionary seed", estonianAddress)
	}
	germanAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "german_stem"})
	german, ok := view.TSDictionary(germanAddress)
	if !ok || germanAddress.ObjectID == 0 || germanAddress == stemAddress || germanAddress == arabicAddress || germanAddress == armenianAddress || germanAddress == basqueAddress || germanAddress == danishAddress || germanAddress == dutchAddress || germanAddress == estonianAddress || germanAddress.ObjectID == 3765 || german.Template != stem.Template {
		t.Fatal("actual distinct German dictionary seed", germanAddress)
	}
	greekAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "greek_stem"})
	greek, ok := view.TSDictionary(greekAddress)
	if !ok || greekAddress.ObjectID == 0 || greekAddress.ObjectID == 3765 || greek.Template != stem.Template {
		t.Fatal("actual Greek dictionary seed", greekAddress)
	}
	for _, earlier := range []ids.ObjectAddress{stemAddress, arabicAddress, armenianAddress, basqueAddress, danishAddress, dutchAddress, estonianAddress, germanAddress} {
		if greekAddress == earlier {
			t.Fatal("Greek dictionary identity reused", greekAddress)
		}
	}
	hindiAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "hindi_stem"})
	hindi, ok := view.TSDictionary(hindiAddress)
	if !ok || hindiAddress.ObjectID == 0 || hindiAddress.ObjectID == 3765 || hindi.Template != stem.Template {
		t.Fatal("actual Hindi dictionary seed", hindiAddress)
	}
	for _, earlier := range []ids.ObjectAddress{stemAddress, arabicAddress, armenianAddress, basqueAddress, danishAddress, dutchAddress, estonianAddress, germanAddress, greekAddress} {
		if hindiAddress == earlier {
			t.Fatal("Hindi dictionary identity reused", hindiAddress)
		}
	}
	hungarianAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "hungarian_stem"})
	hungarian, ok := view.TSDictionary(hungarianAddress)
	if !ok || hungarianAddress.ObjectID == 0 || hungarianAddress.ObjectID == 3765 || hungarian.Template != stem.Template {
		t.Fatal("actual Hungarian dictionary seed", hungarianAddress)
	}
	for _, earlier := range []ids.ObjectAddress{stemAddress, arabicAddress, armenianAddress, basqueAddress, danishAddress, dutchAddress, estonianAddress, germanAddress, greekAddress, hindiAddress} {
		if hungarianAddress == earlier {
			t.Fatal("Hungarian dictionary identity reused", hungarianAddress)
		}
	}
	irishAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "irish_stem"})
	irish, ok := view.TSDictionary(irishAddress)
	if !ok || irishAddress.ObjectID == 0 || irishAddress.ObjectID == 3765 || irish.Template != stem.Template {
		t.Fatal("actual Irish dictionary seed", irishAddress)
	}
	for _, earlier := range []ids.ObjectAddress{stemAddress, arabicAddress, armenianAddress, basqueAddress, danishAddress, dutchAddress, estonianAddress, germanAddress, greekAddress, hindiAddress, hungarianAddress} {
		if irishAddress == earlier {
			t.Fatal("Irish dictionary identity reused", irishAddress)
		}
	}
	italianAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: source.Namespace(), Name: "italian_stem"})
	italian, ok := view.TSDictionary(italianAddress)
	if !ok || italianAddress.ObjectID == 0 || italianAddress.ObjectID == 3765 || italian.Template != stem.Template {
		t.Fatal("actual Italian dictionary seed", italianAddress)
	}
	for _, earlier := range []ids.ObjectAddress{stemAddress, arabicAddress, armenianAddress, basqueAddress, danishAddress, dutchAddress, estonianAddress, germanAddress, greekAddress, hindiAddress, hungarianAddress, irishAddress} {
		if italianAddress == earlier {
			t.Fatal("Italian dictionary identity reused", italianAddress)
		}
	}
	for range 2 {
		got, err := core.CaptureCatalogSourceRows(input, produce)
		if err != nil || got == nil {
			t.Fatal("seed dictionary capture", got, err)
		}
		if got.Ref != ref || got.Anonymous == nil || len(got.Anonymous) != 14 {
			t.Fatal("exact simple/English/Arabic/Armenian/Basque/Danish/Dutch/Estonian/German/Greek/Hindi/Hungarian/Irish/Italian dictionary source inventory", got)
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
			if err != nil || seen[name] || name != "simple" && name != "english_stem" && name != "arabic_stem" && name != "armenian_stem" && name != "basque_stem" && name != "danish_stem" && name != "dutch_stem" && name != "estonian_stem" && name != "german_stem" && name != "greek_stem" && name != "hindi_stem" && name != "hungarian_stem" && name != "irish_stem" && name != "italian_stem" {
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
			} else if name == "arabic_stem" {
				oid, template = uint32(arabicAddress.ObjectID), uint32(arabic.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'arabic'" {
					t.Fatal("Arabic seed language-only options", options, err)
				}
			} else if name == "armenian_stem" {
				oid, template = uint32(armenianAddress.ObjectID), uint32(armenian.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'armenian'" {
					t.Fatal("Armenian seed language-only options", options, err)
				}
			} else if name == "danish_stem" {
				oid, template = uint32(danishAddress.ObjectID), uint32(danish.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'danish', stopwords = 'danish'" {
					t.Fatal("Danish seed ordered options", options, err)
				}
			} else if name == "dutch_stem" {
				oid, template = uint32(dutchAddress.ObjectID), uint32(dutch.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'dutch', stopwords = 'dutch'" {
					t.Fatal("Dutch seed ordered options", options, err)
				}
			} else if name == "estonian_stem" {
				oid, template = uint32(estonianAddress.ObjectID), uint32(estonian.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'estonian'" {
					t.Fatal("Estonian seed language-only options", options, err)
				}
			} else if name == "german_stem" {
				oid, template = uint32(germanAddress.ObjectID), uint32(german.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'german', stopwords = 'german'" {
					t.Fatal("German seed ordered options", options, err)
				}
			} else if name == "greek_stem" {
				oid, template = uint32(greekAddress.ObjectID), uint32(greek.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'greek'" {
					t.Fatal("Greek seed language-only options", options, err)
				}
			} else if name == "hindi_stem" {
				oid, template = uint32(hindiAddress.ObjectID), uint32(hindi.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'hindi'" {
					t.Fatal("Hindi seed language-only options", options, err)
				}
			} else if name == "hungarian_stem" {
				oid, template = uint32(hungarianAddress.ObjectID), uint32(hungarian.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'hungarian', stopwords = 'hungarian'" {
					t.Fatal("Hungarian seed ordered options", options, err)
				}
			} else if name == "irish_stem" {
				oid, template = uint32(irishAddress.ObjectID), uint32(irish.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'irish'" {
					t.Fatal("Irish seed language-only options", options, err)
				}
			} else if name == "italian_stem" {
				oid, template = uint32(italianAddress.ObjectID), uint32(italian.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'italian', stopwords = 'italian'" {
					t.Fatal("Italian seed ordered options", options, err)
				}
			} else {
				oid, template = uint32(basqueAddress.ObjectID), uint32(basque.Template.ObjectID)
				options, err := row[5].Text()
				if err != nil || row[5].IsNull() || options != "language = 'basque'" {
					t.Fatal("Basque seed language-only options", options, err)
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
	empty.RemoveDescriptor(armenianAddress)
	empty.RemoveDescriptor(basqueAddress)
	empty.RemoveDescriptor(danishAddress)
	empty.RemoveDescriptor(dutchAddress)
	empty.RemoveDescriptor(estonianAddress)
	empty.RemoveDescriptor(germanAddress)
	empty.RemoveDescriptor(greekAddress)
	empty.RemoveDescriptor(hindiAddress)
	empty.RemoveDescriptor(hungarianAddress)
	empty.RemoveDescriptor(irishAddress)
	empty.RemoveDescriptor(italianAddress)
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
