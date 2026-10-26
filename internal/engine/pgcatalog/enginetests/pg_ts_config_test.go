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
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 1 {
		t.Fatal("genuine simple configuration inventory", rows, err)
	}
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
	input.Ref = ref
	captured, err := core.CaptureCatalogSourceRows(input, produce)
	if err != nil || captured == nil || captured.Ref != ref || captured.Anonymous == nil || len(captured.Anonymous) != 1 {
		t.Fatal("configuration capture lost source identity", captured, err)
	}
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
	if rows, err := produce(input); rows == nil || len(rows) != 1 || err != nil {
		t.Fatal("replacement configuration capture", rows, err)
	}
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
	if err != nil || len(rows) != 19 {
		t.Fatal("actual simple mappings", rows, err)
	}
	tokens := map[int64]bool{}
	for _, row := range rows {
		if len(row) != 4 {
			t.Fatal("mapping shape", row)
		}
		values := [4]int64{}
		for i, v := range row {
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
		if values[0] != 3748 || values[2] != 1 || values[3] != 3765 || tokens[values[1]] {
			t.Fatal("mapping identity/sequence/dictionary", values)
		}
		tokens[values[1]] = true
	}
	for _, token := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
		if !tokens[token] {
			t.Fatal("missing stock token", token)
		}
	}
	next := *view.MustTSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748})
	next.Mappings = nil
	next.ObjectRevision++
	state := catalog.CloneState(view.State())
	state.SetDescriptor(next.Address(), &next)
	input.Catalog = view.WithState(state)
	if fresh, err := produce(input); err != nil || fresh == nil || len(fresh) != 0 || len(rows) != 19 {
		t.Fatal("capture retained mapping rows", fresh, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = canceled
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("mapping capture cancellation", rows, err)
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
