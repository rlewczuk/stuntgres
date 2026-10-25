package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
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
	e := New()
	e.Storage.SetFrontierLocked(100, 200)
	e.Catalog.PublishLocked(catalog)
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(parent.Address(), source)
	e.Mu.Unlock()
	e.Mu.Lock()
	e.Storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): original})
	e.Mu.Unlock()
	e.Visibility.SetCommitSeqLocked(1)
	snapshot, err := EngineCaptureIndexSnapshot(e, ctx, e.Catalog.Read(catalog), map[ids.ObjectAddress]*storage.Relation{parent.Address(): source}, map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): original}, 1)
	if err != nil {
		t.Fatal(err)
	}
	roots, rows, versions, err := EnginePrepareRemintedStorage(e, ctx, catalog, map[ids.ObjectAddress]*storage.Relation{parent.Address(): destination}, snapshot, true)
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
	if _, _, _, err := EnginePrepareRemintedStorage(e, canceled, catalog, map[ids.ObjectAddress]*storage.Relation{parent.Address(): destination}, snapshot, true); !errors.Is(err, context.Canceled) {
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
	e := New()
	// This is a deliberately reduced but valid key-construction catalogue:
	// namespace descriptors are needed for UNIQUE diagnostics, not nonunique
	// ordering/provider/shape validation or replacement materialization.
	roots, err := EnginePrepareIndexRoots(e, &exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, snapshot, snapshot, relations, nil, nil, nil, nil, false, sqltype.OutputEnvironment{}, nil, nil, nil)
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
		if _, err := EnginePrepareIndexRoots(e, &exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, snapshot, invalid, relations, nil, nil, nil, nil, materialize, sqltype.OutputEnvironment{}, nil, nil, nil); err == nil {
			t.Fatal("phase switch weakened actual index definition validation", materialize)
		}
	}
}
