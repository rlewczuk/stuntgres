package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func assertSeedConfigurations(t *testing.T, view catalog.Read, source *catalog.Relation, rows [][]sqltype.Value) {
	t.Helper()
	if len(rows) != 10 {
		t.Fatal("exact simple/English/Arabic/Armenian/Basque/Danish/Dutch/Estonian/German/Greek configuration inventory", rows)
	}
	english := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "english"})
	if english.ObjectID == 0 || english.ObjectID == 3748 {
		t.Fatal("actual allocated English configuration", english)
	}
	arabic := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "arabic"})
	if arabic.ObjectID == 0 || arabic.ObjectID == 3748 || arabic == english {
		t.Fatal("actual allocated Arabic configuration", arabic)
	}
	armenian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "armenian"})
	if armenian.ObjectID == 0 || armenian.ObjectID == 3748 || armenian == english || armenian == arabic {
		t.Fatal("actual allocated Armenian configuration", armenian)
	}
	basque := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "basque"})
	if basque.ObjectID == 0 || basque.ObjectID == 3748 || basque == english || basque == arabic || basque == armenian {
		t.Fatal("actual allocated Basque configuration", basque)
	}
	danish := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "danish"})
	if danish.ObjectID == 0 || danish.ObjectID == 3748 || danish == english || danish == arabic || danish == armenian || danish == basque {
		t.Fatal("actual allocated Danish configuration", danish)
	}
	dutch := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "dutch"})
	if dutch.ObjectID == 0 || dutch.ObjectID == 3748 || dutch == english || dutch == arabic || dutch == armenian || dutch == basque || dutch == danish {
		t.Fatal("actual allocated Dutch configuration", dutch)
	}
	estonian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "estonian"})
	if estonian.ObjectID == 0 || estonian.ObjectID == 3748 || estonian == english || estonian == arabic || estonian == armenian || estonian == basque || estonian == danish || estonian == dutch {
		t.Fatal("actual allocated Estonian configuration", estonian)
	}
	german := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "german"})
	if german.ObjectID == 0 || german.ObjectID == 3748 || german == english || german == arabic || german == armenian || german == basque || german == danish || german == dutch || german == estonian {
		t.Fatal("actual allocated German configuration", german)
	}
	greek := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "greek"})
	if greek.ObjectID == 0 || greek.ObjectID == 3748 || greek == english || greek == arabic || greek == armenian || greek == basque || greek == danish || greek == dutch || greek == estonian || greek == german {
		t.Fatal("actual allocated Greek configuration", greek)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if len(row) != 5 {
			t.Fatal("configuration row width", row)
		}
		for i, value := range row {
			if value.IsNull() || value.Type() != source.Columns[i].Usage.Type {
				t.Fatal("source-owned configuration cell", i, value)
			}
		}
		name, err := row[1].Text()
		if err != nil || seen[name] || name != "simple" && name != "english" && name != "arabic" && name != "armenian" && name != "basque" && name != "danish" && name != "dutch" && name != "estonian" && name != "german" && name != "greek" {
			t.Fatal("configuration identity", name, err)
		}
		seen[name] = true
		want := uint32(3748)
		if name == "english" {
			want = uint32(english.ObjectID)
		}
		if name == "arabic" {
			want = uint32(arabic.ObjectID)
		}
		if name == "armenian" {
			want = uint32(armenian.ObjectID)
		}
		if name == "basque" {
			want = uint32(basque.ObjectID)
		}
		if name == "danish" {
			want = uint32(danish.ObjectID)
		}
		if name == "dutch" {
			want = uint32(dutch.ObjectID)
		}
		if name == "estonian" {
			want = uint32(estonian.ObjectID)
		}
		if name == "german" {
			want = uint32(german.ObjectID)
		}
		if name == "greek" {
			want = uint32(greek.ObjectID)
		}
		for i, expected := range map[int]uint32{0: want, 2: 11, 3: 10, 4: 3722} {
			got, err := row[i].OID()
			if err != nil || got != expected {
				t.Fatal("configuration seed relationship", name, i, got, expected, err)
			}
		}
	}
}

func TestPGTSConfigCapture(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TSConfigOID}
	source := view.MustRelation(address)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.TSConfigOID)
	if !ok {
		t.Fatal("missing configuration producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}
	rows, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	assertSeedConfigurations(t, view, source, rows)
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
	input.Ref = ref
	captured, err := core.CaptureCatalogSourceRows(input, produce)
	if err != nil || captured == nil || captured.Ref != ref || captured.Anonymous == nil || len(captured.Anonymous) != 10 {
		t.Fatal("configuration capture lost source identity", captured, err)
	}
	assertSeedConfigurations(t, view, source, captured.Anonymous)
	for _, width := range []int{4, 6} {
		invalid := *source
		invalid.Columns = make([]catalog.Column, width)
		copy(invalid.Columns, source.Columns)
		input.Source = &invalid
		if rows, err := produce(input); rows != nil || err == nil {
			t.Fatal("invalid source width admitted", width, rows, err)
		}
	}
	foreign := engine.New().CatalogRead()
	for i, column := range source.Columns {
		invalid := *source
		invalid.Columns = append([]catalog.Column(nil), source.Columns...)
		invalid.Columns[i].Usage.Type, _ = foreign.LookupType(column.Usage.Type.ID())
		input.Source = &invalid
		if rows, err := produce(input); rows != nil || err == nil {
			t.Fatal("foreign source type admitted", i, rows, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = ctx
	// Cancellation takes precedence even over a malformed capture shape.
	invalid := *source
	invalid.Columns = nil
	input.Source = &invalid
	if rows, err := produce(input); rows != nil || err != context.Canceled {
		t.Fatal("canceled producer", rows, err)
	}
	if got, err := core.CaptureCatalogSourceRows(input, produce); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published source", got, err)
	}
}

func TestPGTSConfigOwnership(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TSConfigOID}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		relation := view.MustRelation(address)
		if relation.RowType != 10064 || len(relation.Columns) != 5 {
			t.Fatal("native configuration identity/width", relation.RowType, len(relation.Columns))
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10064})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10063})
		ownedRow, rowOK := view.LookupType(10064)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || ownedRow != row.SQLType || ownedArray != array.SQLType || row.Array != 10063 || array.SQLType.Element() != row.SQLType || row.OwningRelation != address || array.OwningRelation != address || row.Owner != relation.Owner || array.Owner != relation.Owner {
			t.Fatal("native configuration row/array ownership", row, array)
		}
		for i, column := range relation.Columns {
			id := sqltype.Oid
			if i == 1 {
				id = sqltype.Name
			}
			owned, ok := view.LookupType(id)
			if !ok || owned != column.Usage.Type {
				t.Fatal("foreign configuration attribute", i)
			}
		}
		return relation
	}
	oldView := e.CatalogRead()
	old := check(oldView)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldView.LookupType(10064)
	oldArray, _ := oldView.LookupArray(oldRow)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	freshView := e.CatalogRead()
	fresh := check(freshView)
	if old == fresh || old.Columns[0].Usage.Type == fresh.Columns[0].Usage.Type || old.Columns[1].Usage.Type == fresh.Columns[1].Usage.Type {
		t.Fatal("replacement reused configuration descriptors")
	}
	freshRow, _ := freshView.LookupType(10064)
	freshArray, _ := freshView.LookupArray(freshRow)
	if freshRow == oldRow || freshArray == oldArray {
		t.Fatal("replacement reused configuration row/array owner")
	}
	if !reflect.DeepEqual(old.Columns, columns) || oldArray.Element() != oldRow || check(oldView) != old {
		t.Fatal("replacement changed retained configuration descriptors")
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.TSConfigOID)
	if !ok {
		t.Fatal("missing configuration producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: freshView, Source: fresh, Owner: freshView.Owner(), Runtime: core.EngineCatalogRuntime{}}
	rows, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	assertSeedConfigurations(t, freshView, fresh, rows)
	input.Source = old
	if rows, err := produce(input); rows != nil || err == nil {
		t.Fatal("old generation configuration source admitted", rows, err)
	}
}

func TestPGTSConfigMapCapture(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TSConfigMapOID})
	produce, ok := pgcatalog.ProducerFor(pgcatalog.TSConfigMapOID)
	if !ok {
		t.Fatal("missing mapping producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}
	rows, err := produce(input)
	if err != nil || len(rows) != 190 {
		t.Fatal("actual simple, English, Arabic, Armenian, Basque, Danish, Dutch, Estonian, German and Greek mappings", rows, err)
	}
	english := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "english"})
	stem := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "english_stem"})
	if english.ObjectID == 0 || stem.ObjectID == 0 {
		t.Fatal("missing allocated English identities")
	}
	arabic := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "arabic"})
	arabicStem := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "arabic_stem"})
	if arabic.ObjectID == 0 || arabicStem.ObjectID == 0 || arabic == english || arabicStem == stem {
		t.Fatal("missing distinct Arabic identities")
	}
	armenian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "armenian"})
	armenianStem := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "armenian_stem"})
	if armenian.ObjectID == 0 || armenianStem.ObjectID == 0 || armenian == english || armenian == arabic || armenianStem == stem || armenianStem == arabicStem {
		t.Fatal("missing distinct Armenian identities")
	}
	basque := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "basque"})
	basqueStem := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "basque_stem"})
	if basque.ObjectID == 0 || basqueStem.ObjectID == 0 || basque.ObjectID == 3748 || basqueStem.ObjectID == 3765 || basque == english || basque == arabic || basque == armenian || basqueStem == stem || basqueStem == arabicStem || basqueStem == armenianStem {
		t.Fatal("missing distinct Basque identities")
	}
	danish := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "danish"})
	danishStem := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "danish_stem"})
	if danish.ObjectID == 0 || danishStem.ObjectID == 0 || danish.ObjectID == 3748 || danishStem.ObjectID == 3765 || danish == english || danish == arabic || danish == armenian || danish == basque || danishStem == stem || danishStem == arabicStem || danishStem == armenianStem || danishStem == basqueStem {
		t.Fatal("missing distinct Danish identities")
	}
	dutch := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "dutch"})
	dutchStem := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "dutch_stem"})
	if dutch.ObjectID == 0 || dutchStem.ObjectID == 0 || dutch.ObjectID == 3748 || dutchStem.ObjectID == 3765 || dutch == english || dutch == arabic || dutch == armenian || dutch == basque || dutch == danish || dutchStem == stem || dutchStem == arabicStem || dutchStem == armenianStem || dutchStem == basqueStem || dutchStem == danishStem {
		t.Fatal("missing distinct Dutch identities")
	}
	estonian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "estonian"})
	estonianStem := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "estonian_stem"})
	if estonian.ObjectID == 0 || estonianStem.ObjectID == 0 || estonian.ObjectID == 3748 || estonianStem.ObjectID == 3765 || estonian == english || estonian == arabic || estonian == armenian || estonian == basque || estonian == danish || estonian == dutch || estonianStem == stem || estonianStem == arabicStem || estonianStem == armenianStem || estonianStem == basqueStem || estonianStem == danishStem || estonianStem == dutchStem {
		t.Fatal("missing distinct Estonian identities")
	}
	german := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "german"})
	germanStem := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "german_stem"})
	if german.ObjectID == 0 || germanStem.ObjectID == 0 || german.ObjectID == 3748 || germanStem.ObjectID == 3765 || german == english || german == arabic || german == armenian || german == basque || german == danish || german == dutch || german == estonian || germanStem == stem || germanStem == arabicStem || germanStem == armenianStem || germanStem == basqueStem || germanStem == danishStem || germanStem == dutchStem || germanStem == estonianStem {
		t.Fatal("missing distinct German identities")
	}
	greek := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "greek"})
	greekStem := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "greek_stem"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if greek.ObjectID == 0 || greek == earlier {
			t.Fatal("missing distinct Greek configuration", greek)
		}
	}
	for _, earlier := range []ids.ObjectAddress{stem, arabicStem, armenianStem, basqueStem, danishStem, dutchStem, estonianStem, germanStem, {ClassID: ids.TSDictionaryClassID, ObjectID: 3765}} {
		if greekStem.ObjectID == 0 || greekStem == earlier {
			t.Fatal("missing distinct Greek dictionary", greekStem)
		}
	}
	tokens := map[[2]int64]bool{}
	for _, row := range rows {
		if len(row) != 4 {
			t.Fatal("mapping shape", row)
		}
		values := [4]int64{}
		for i, v := range row {
			if v.IsNull() || v.Type() != source.Columns[i].Usage.Type {
				t.Fatal("mapping cell lost source-owned type", i, v)
			}
			if i == 0 || i == 3 {
				oid, failure := v.OID()
				err = failure
				values[i] = int64(oid)
			} else {
				values[i], err = v.Integer()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		key := [2]int64{values[0], values[1]}
		wantDictionary := int64(3765)
		if values[0] == int64(english.ObjectID) {
			switch values[1] {
			case 1, 2, 10, 11, 16, 17:
				wantDictionary = int64(stem.ObjectID)
			}
		} else if values[0] == int64(arabic.ObjectID) {
			switch values[1] {
			case 1, 2, 10, 11, 16, 17:
				wantDictionary = int64(arabicStem.ObjectID)
			}
		} else if values[0] == int64(armenian.ObjectID) {
			switch values[1] {
			case 1, 2, 10, 11, 16, 17:
				wantDictionary = int64(armenianStem.ObjectID)
			}
		} else if values[0] == int64(basque.ObjectID) {
			switch values[1] {
			case 1, 2, 10, 11, 16, 17:
				wantDictionary = int64(basqueStem.ObjectID)
			}
		} else if values[0] == int64(danish.ObjectID) {
			switch values[1] {
			case 1, 2, 10, 11, 16, 17:
				wantDictionary = int64(danishStem.ObjectID)
			}
		} else if values[0] == int64(dutch.ObjectID) {
			switch values[1] {
			case 1, 2, 10, 11, 16, 17:
				wantDictionary = int64(dutchStem.ObjectID)
			}
		} else if values[0] == int64(estonian.ObjectID) {
			switch values[1] {
			case 1, 2, 10, 11, 16, 17:
				wantDictionary = int64(estonianStem.ObjectID)
			}
		} else if values[0] == int64(german.ObjectID) {
			switch values[1] {
			case 1, 2, 10, 11, 16, 17:
				wantDictionary = int64(germanStem.ObjectID)
			}
		} else if values[0] == int64(greek.ObjectID) {
			switch values[1] {
			case 1, 2, 10, 11, 16, 17:
				wantDictionary = int64(greekStem.ObjectID)
			}
		} else if values[0] != 3748 {
			t.Fatal("unexpected configuration mapping", values)
		}
		if values[2] != 1 || values[3] != wantDictionary || tokens[key] {
			t.Fatal("mapping identity/sequence/dictionary", values)
		}
		tokens[key] = true
	}
	for _, config := range []int64{3748, int64(english.ObjectID), int64(arabic.ObjectID), int64(armenian.ObjectID), int64(basque.ObjectID), int64(danish.ObjectID), int64(dutch.ObjectID), int64(estonian.ObjectID), int64(german.ObjectID), int64(greek.ObjectID)} {
		for _, token := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
			if !tokens[[2]int64{config, token}] {
				t.Fatal("missing stock token", config, token)
			}
		}
	}
	retained := make([][]sqltype.Value, len(rows))
	for i := range rows {
		retained[i] = append([]sqltype.Value(nil), rows[i]...)
	}
	address := source.Address()
	input.Ref = e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
	captured, err := core.CaptureCatalogSourceRows(input, produce)
	if err != nil || captured == nil || captured.Ref != input.Ref || captured.Anonymous == nil || !reflect.DeepEqual(captured.Anonymous, rows) {
		t.Fatal("mapping capture lost source identity/rows", captured, err)
	}
	next := *view.MustTSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748})
	next.Mappings = nil
	next.ObjectRevision++
	state := catalog.CloneState(view.State())
	state.SetDescriptor(next.Address(), &next)
	nextEnglish := *view.MustTSConfiguration(english)
	nextEnglish.Mappings = nil
	nextEnglish.ObjectRevision++
	state.SetDescriptor(english, &nextEnglish)
	nextArabic := *view.MustTSConfiguration(arabic)
	nextArabic.Mappings = nil
	nextArabic.ObjectRevision++
	state.SetDescriptor(arabic, &nextArabic)
	nextArmenian := *view.MustTSConfiguration(armenian)
	nextArmenian.Mappings = nil
	nextArmenian.ObjectRevision++
	state.SetDescriptor(armenian, &nextArmenian)
	nextBasque := *view.MustTSConfiguration(basque)
	nextBasque.Mappings = nil
	nextBasque.ObjectRevision++
	state.SetDescriptor(basque, &nextBasque)
	nextDanish := *view.MustTSConfiguration(danish)
	nextDanish.Mappings = nil
	nextDanish.ObjectRevision++
	state.SetDescriptor(danish, &nextDanish)
	nextDutch := *view.MustTSConfiguration(dutch)
	nextDutch.Mappings = nil
	nextDutch.ObjectRevision++
	state.SetDescriptor(dutch, &nextDutch)
	nextEstonian := *view.MustTSConfiguration(estonian)
	nextEstonian.Mappings = nil
	nextEstonian.ObjectRevision++
	state.SetDescriptor(estonian, &nextEstonian)
	nextGerman := *view.MustTSConfiguration(german)
	nextGerman.Mappings = nil
	nextGerman.ObjectRevision++
	state.SetDescriptor(german, &nextGerman)
	nextGreek := *view.MustTSConfiguration(greek)
	nextGreek.Mappings = nil
	nextGreek.ObjectRevision++
	state.SetDescriptor(greek, &nextGreek)
	input.Catalog = view.WithState(state)
	if fresh, err := produce(input); err != nil || fresh == nil || len(fresh) != 0 || !reflect.DeepEqual(rows, retained) {
		t.Fatal("capture retained exact mapping rows", fresh, err)
	}
	empty, err := core.CaptureCatalogSourceRows(input, produce)
	if err != nil || empty == nil || empty.Ref != input.Ref || empty.Anonymous == nil || len(empty.Anonymous) != 0 {
		t.Fatal("empty capture lost anonymous source identity", empty, err)
	}
	reject := func(label string) {
		t.Helper()
		if rows, err := produce(input); rows != nil || err == nil {
			t.Fatal(label, rows, err)
		}
		if got, err := core.CaptureCatalogSourceRows(input, produce); got != nil || err == nil {
			t.Fatal("wrapper published invalid "+label, got, err)
		}
	}
	for _, width := range []int{3, 5} {
		invalid := *source
		invalid.Columns = make([]catalog.Column, width)
		copy(invalid.Columns, source.Columns)
		input.Source = &invalid
		reject("invalid mapping width")
	}
	foreign := engine.New().CatalogRead()
	for i, column := range source.Columns {
		invalid := *source
		invalid.Columns = append([]catalog.Column(nil), source.Columns...)
		invalid.Columns[i].Usage.Type, _ = foreign.LookupType(column.Usage.Type.ID())
		input.Source = &invalid
		reject("foreign mapping column type")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = canceled
	invalid := *source
	invalid.Columns = nil
	input.Source = &invalid
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("mapping capture cancellation", rows, err)
	}
	if got, err := core.CaptureCatalogSourceRows(input, produce); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled mapping wrapper published source", got, err)
	}
}

func TestPGTSConfigMapOwnership(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TSConfigMapOID}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		relation := view.MustRelation(address)
		if relation.Address() != address || relation.RowType != 10066 || len(relation.Columns) != 4 {
			t.Fatal("native mapping identity/shape", relation)
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10066})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10065})
		ownedRow, rowOK := view.LookupType(10066)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || ownedRow != row.SQLType || ownedArray != array.SQLType || row.Array != 10065 || array.SQLType.Element() != row.SQLType || row.OwningRelation != address || array.OwningRelation != address || row.Owner != relation.Owner || array.Owner != relation.Owner {
			t.Fatal("native mapping row/array ownership", row, array)
		}
		for i, name := range []string{"mapcfg", "maptokentype", "mapseqno", "mapdict"} {
			id := sqltype.Oid
			if i == 1 || i == 2 {
				id = sqltype.Int4
			}
			owned, ok := view.LookupType(id)
			if !ok || relation.Columns[i].Name != name || relation.Columns[i].Usage.Type != owned {
				t.Fatal("mapping source attribute identity", i, relation.Columns[i])
			}
		}
		return relation
	}
	oldView := e.CatalogRead()
	old := check(oldView)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldView.LookupType(10066)
	oldArray, _ := oldView.LookupArray(oldRow)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.TSConfigMapOID)
	if !ok {
		t.Fatal("missing mapping producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: oldView, Source: old, Owner: oldView.Owner(), Runtime: core.EngineCatalogRuntime{}}
	rows, err := produce(input)
	if err != nil || len(rows) != 190 {
		t.Fatal("old generation mapping rows", rows, err)
	}
	retained := make([][]sqltype.Value, len(rows))
	for i := range rows {
		retained[i] = append([]sqltype.Value(nil), rows[i]...)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	freshView := e.CatalogRead()
	fresh := check(freshView)
	freshRow, _ := freshView.LookupType(10066)
	freshArray, _ := freshView.LookupArray(freshRow)
	if old == fresh || oldRow == freshRow || oldArray == freshArray {
		t.Fatal("replacement reused mapping relation/row/array")
	}
	for i := range columns {
		if columns[i].Usage.Type == fresh.Columns[i].Usage.Type {
			t.Fatal("replacement reused mapping attribute type", i)
		}
		for _, row := range rows {
			if row[i].Type() != columns[i].Usage.Type || row[i].Type() == fresh.Columns[i].Usage.Type {
				t.Fatal("retained mapping cell retagged", i)
			}
		}
	}
	if !reflect.DeepEqual(old.Columns, columns) || !reflect.DeepEqual(rows, retained) || oldArray.Element() != oldRow || check(oldView) != old {
		t.Fatal("replacement changed retained mapping generation")
	}
	input.Catalog, input.Owner, input.Source = freshView, freshView.Owner(), fresh
	if got, err := produce(input); err != nil || got == nil || len(got) != 190 {
		t.Fatal("fresh mapping producer", got, err)
	}
	input.Source = old
	if got, err := produce(input); got != nil || err == nil {
		t.Fatal("fresh mapping producer admitted old source", got, err)
	}
	if got, err := core.CaptureCatalogSourceRows(input, produce); got != nil || err == nil {
		t.Fatal("wrapper admitted old mapping source", got, err)
	}
}

func TestPGTSConfigSeedDependencies(t *testing.T) {
	e := engine.New()
	original := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748}
	seed := original.MustTSConfiguration(address)
	state := catalog.CloneState(original.State())
	dictionary := *original.MustTSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765})
	dictionary.ObjectAddress.ObjectID = 48001
	dictionary.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	dictionary.ObjectName = "config_seed_dict"
	state.SetDescriptor(dictionary.Address(), &dictionary)
	state.BindName(catalog.DescriptorNameKey(&dictionary), dictionary.Address())
	view := original.WithState(state)
	if err := view.Owner().Providers[ids.TSDictionaryClassID].Validate(view, &dictionary); err != nil {
		t.Fatal("custom dictionary authority", err)
	}
	check := func(want []pgcatalog.DependEdge) {
		t.Helper()
		current := view.MustTSConfiguration(address)
		if err := (catalog.TSConfigurationProvider{}).Validate(view, current); err != nil {
			t.Fatal("configuration authority", err)
		}
		edges, err := pgcatalog.CollectDepend(context.Background(), view, core.EngineCatalogRuntime{})
		if err != nil {
			t.Fatal(err)
		}
		var actual []pgcatalog.DependEdge
		for _, edge := range edges {
			if edge.Dependent == address {
				actual = append(actual, edge)
			}
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("seed dependencies got %#v, want %#v", actual, want)
		}
	}
	check(nil)
	next := *seed
	next.Mappings = catalog.CloneTSMappings(seed.Mappings)
	next.ObjectRevision++
	for i := range next.Mappings {
		if next.Mappings[i].TokenID == 1 || next.Mappings[i].TokenID == 3 {
			next.Mappings[i].Dictionaries = []ids.ObjectAddress{dictionary.Address(), dictionary.Address()}
		}
	}
	state.SetDescriptor(address, &next)
	one := []pgcatalog.DependEdge{{Dependent: address, Referenced: dictionary.Address(), DepType: 'n'}}
	check(one)
	for _, token := range []int32{1, 3} {
		for i := range next.Mappings {
			if next.Mappings[i].TokenID == token {
				next.Mappings[i].Dictionaries = seed.Mapping(token)
			}
		}
		if token == 1 {
			check(one)
		} else {
			check(nil)
		}
	}
}
