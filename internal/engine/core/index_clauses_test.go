package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestIndexClausesReplacement(t *testing.T) {
	parent, idx, catalog, types := indexClausesFixture(t)
	ctx := context.Background()
	one, _ := sqltype.Integer(types[sqltype.Int4], 1)
	source := &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 2, Born: 1, Values: []sqltype.Value{one, one, sqltype.String(types[sqltype.Text], "payload")}}}}
	original, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	destination := &storage.Relation{Versions: append([]storage.RowVersion(nil), source.Versions...)}
	e := engine.New()
	e.Storage.SetFrontierLocked(100, 200)
	e.Catalog.PublishLocked(catalog)
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(parent.Address(), source)
	e.Mu.Unlock()
	e.Mu.Lock()
	e.Storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): original})
	e.Mu.Unlock()
	e.Visibility.SetCommitSeqLocked(1)
	snapshot, err := core.EngineCaptureIndexSnapshot(e, ctx, e.Catalog.Read(catalog), map[ids.ObjectAddress]*storage.Relation{parent.Address(): source}, map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): original}, 1)
	if err != nil {
		t.Fatal(err)
	}
	roots, rows, versions, err := core.EnginePrepareRemintedStorage(e, ctx, catalog, map[ids.ObjectAddress]*storage.Relation{parent.Address(): destination}, snapshot, true)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 101 || versions != 201 || roots[idx.Address()].Entries[0].Row != 101 || len(roots[idx.Address()].Entries[0].Keys) != 2 || e.Storage.RowFrontier() != 100 || e.Storage.VersionFrontier() != 200 {
		t.Fatal("replacement did not privately remint actual key-only root", roots, rows, versions)
	}
	if !reflect.DeepEqual(idx.SQLIndex.Include, []int32{30}) || source.Versions[0].Row != 1 || original.Entries[0].Version != 2 {
		t.Fatal("replacement mutated source")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := core.EnginePrepareRemintedStorage(e, canceled, catalog, map[ids.ObjectAddress]*storage.Relation{parent.Address(): destination}, snapshot, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.Storage.RowFrontier() != 100 || e.Storage.VersionFrontier() != 200 {
		t.Fatal("failed replacement published allocator")
	}
}

func TestIndexClausesValidationOnlyReplacement(t *testing.T) {
	parent, index, snapshot, types := indexClausesFixture(t)
	one, err := sqltype.Integer(types[sqltype.Int4], 1)
	if err != nil {
		t.Fatal(err)
	}
	relations := map[ids.ObjectAddress]*storage.Relation{parent.Address(): {Versions: []storage.RowVersion{{Row: 1, Version: 2, Born: 1, Values: []sqltype.Value{one, one, sqltype.String(types[sqltype.Text], "payload")}}}}}
	e := engine.New()
	// This is a deliberately reduced but valid key-construction catalogue:
	// namespace descriptors are needed for UNIQUE diagnostics, not nonunique
	// ordering/provider/shape validation or replacement materialization.
	roots, err := core.EnginePrepareIndexRoots(e, &exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, snapshot, snapshot, relations, nil, nil, nil, nil, false, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || roots != nil {
		t.Fatal("validation-only replacement required unrelated namespace metadata or materialized roots", roots, err)
	}
	broken := *index
	definition := *index.SQLIndex
	definition.Keys = append([]catalog.IndexKey(nil), index.SQLIndex.Keys...)
	definition.Keys[0].Attribute = 999
	broken.SQLIndex = &definition
	invalid := catalogStateFixture(0, map[ids.ObjectAddress]catalog.Descriptor{parent.Address(): parent, index.Address(): &broken}, nil)
	for _, materialize := range []bool{false, true} {
		if _, err := core.EnginePrepareIndexRoots(e, &exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, snapshot, invalid, relations, nil, nil, nil, nil, materialize, sqltype.OutputEnvironment{}, nil, nil, nil); err == nil {
			t.Fatal("phase switch weakened actual index definition validation", materialize)
		}
	}
}

func indexClausesFixture(t *testing.T) (*catalog.Relation, *catalog.Relation, *catalog.State, map[sqltype.ID]*sqltype.Descriptor) {
	t.Helper()
	types := indexClausesTypes()
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "t", ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{10, 20, 30}, ColumnMeta: catalog.ColumnMetadata{HighWater: 30}, Columns: []catalog.Column{{Name: "a", Usage: types[sqltype.Int4].Usage()}, {Name: "b", Usage: types[sqltype.Int4].Usage()}, {Name: "p", Usage: types[sqltype.Text].Usage()}}, Index: map[string]int{"a": 0, "b": 1, "p": 2}}
	c, ok, defaultErr := operatorFamilyTestRead().DefaultIndexClass(403, types[sqltype.Int4])
	if defaultErr != nil {
		t.Fatal(defaultErr)
	}
	if !ok {
		t.Fatal("missing current integer default class")
	}
	def := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 10, Class: c}, {Attribute: 20, Class: c, Desc: true, NullsFirst: true}}, Include: []int32{30}, Ready: true, Valid: true, Live: true}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, ObjectNamespace: parent.Namespace(), ObjectName: "cover", ObjectRevision: 1, RelationKind: catalog.IndexRelation, SQLIndex: def, Columns: append([]catalog.Column(nil), parent.Columns...), Attributes: []int32{1, 2, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, Index: map[string]int{"a": 0, "b": 1, "p": 2}}
	catalogSnapshot := operatorFamilyFixtureState(catalogStateFixture(0, map[ids.ObjectAddress]catalog.Descriptor{parent.Address(): parent, idx.Address(): idx}, nil))
	if err := index.ValidateIndexDefinition(catalog.NewRead(nil, catalogSnapshot), idx); err != nil {
		t.Fatal(err)
	}
	return parent, idx, catalogSnapshot, types
}

// catalogStateFixture builds an unpublished state at epoch that holds
// descriptors and names, through the state's write API.
func catalogStateFixture(epoch ids.CatalogEpoch, descriptors map[ids.ObjectAddress]catalog.Descriptor, names map[ids.NameKey]ids.ObjectAddress) *catalog.State {
	s := catalog.NewState(epoch)
	for a, d := range descriptors {
		s.SetDescriptor(a, d)
	}
	for k, a := range names {
		s.BindName(k, a)
	}
	return s
}

func indexClausesTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}

func operatorFamilyFixtureState(state *catalog.State) *catalog.State {
	for address, descriptor := range operatorFamilyTestRead().Descriptors() {
		switch address.ClassID {
		case ids.AccessMethodClassID, ids.OperatorFamilyClassID, ids.OperatorClassCatalogID, ids.AccessMethodOperatorClassID, ids.AccessMethodProcedureClassID, ids.ProcedureClassID, ids.OperatorClassID:
			state.SetDescriptor(address, descriptor)
			state.BindName(catalog.DescriptorNameKey(descriptor), address)
		}
	}
	return state
}
