package enginetests_test

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestBtreeGINNetworkTransport(t *testing.T) {
	e, idx, root := networkEngine(t, true)
	view := e.CatalogRead()
	parent := view.MustRelation(idx.SQLIndex.Parent)
	store := e.Storage.Relations[parent.Address()]
	before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
	eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	baseline := &storage.Relation{}
	positions := make(map[index.RowIdentity]int)
	for i, pos := range []int{0, 3} {
		row := store.Versions[pos]
		positions[index.RowIdentity{Row: row.Row, Version: row.Version}] = i
		baseline.Versions = append(baseline.Versions, row)
	}
	build := &index.AlterIndexBuild{Index: idx, Root: root, Store: store}
	filtered, err := build.BaselineRoot(eval, idx, baseline, positions)
	if err != nil {
		t.Fatal(err)
	}
	ginAssertOwners(t, filtered, baseline)
	if len(filtered.Entries) != 2 || filtered.Construction != root.Construction {
		t.Fatal("baseline lost construction history")
	}
	for col, tc := range networkPrivateCases {
		value := ginInput(t, filtered.Gin.Columns[col].Binding.Usage.Type, tc.upper)
		got, err := index.GinScalarCandidates(context.Background(), filtered.Gin.Columns[col], value, 3, sqltype.InputEnvironment{})
		row := baseline.Versions[1]
		if err != nil || !slices.Equal(got, []storage.GinPosting{{Row: row.Row, Version: row.Version, Position: 1}}) {
			t.Fatal("baseline physical slots", tc.typ, got, err)
		}
	}
	stores := map[ids.ObjectAddress]*storage.Relation{parent.Address(): store}
	roots := map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): root}
	snapshot, err := index.CaptureSnapshot(context.Background(), index.SnapshotCapture{View: view, Authority: view.State(), Relations: stores, Roots: roots, Frontier: e.Visibility.CommitSeq(), Store: func(a ids.ObjectAddress) *storage.Relation { return stores[a] }, Root: func(a ids.ObjectAddress) *storage.IndexRoot { return roots[a] }})
	if err != nil {
		t.Fatal(err)
	}
	destination := &storage.Relation{Versions: slices.Clone(store.Versions)}
	physical := make(map[index.RowIdentity]int)
	for i := range destination.Versions {
		row := &destination.Versions[i]
		physical[index.RowIdentity{Row: row.Row, Version: row.Version}] = i
		row.Row = ids.RowID(1000 + i)
		row.Version = ids.VersionID(2000 + i)
	}
	transported, err := snapshot.Transport(context.Background(), view, map[ids.ObjectAddress]*storage.Relation{parent.Address(): destination}, map[ids.ObjectAddress]map[index.RowIdentity]int{parent.Address(): physical})
	if err != nil {
		t.Fatal(err)
	}
	moved := transported[idx.Address()]
	ginAssertOwners(t, moved, destination)
	if moved == root || moved.Gin == root.Gin || moved.Construction == root.Construction || !index.ConstructionMatches(moved, idx) {
		t.Fatal("snapshot lost destination construction")
	}
	for col, tc := range networkPrivateCases {
		query := ginInput(t, moved.Gin.Columns[col].Binding.Usage.Type, tc.boundary)
		got, err := index.GinScalarCandidates(context.Background(), moved.Gin.Columns[col], query, 3, sqltype.InputEnvironment{})
		want := []storage.GinPosting{{Row: 1001, Version: 2001, Position: 1}, {Row: 1002, Version: 2002, Position: 2}}
		if err != nil || !slices.Equal(got, want) {
			t.Fatal("remint exact triples", tc.typ, got, err)
		}
	}
	targetParent := *parent
	integer, _ := view.LookupType(sqltype.Int4)
	targetParent.Attributes = append([]int32{99}, parent.Attributes...)
	targetParent.Columns = append([]catalog.Column{{Name: "padding", Usage: integer.Usage()}}, parent.Columns...)
	targetStore := &storage.Relation{Versions: slices.Clone(store.Versions)}
	for i := range targetStore.Versions {
		targetStore.Versions[i].Values = append([]sqltype.Value{sqltype.Null(integer)}, store.Versions[i].Values...)
	}
	targetIndex := *idx
	journal := &session.Journal{}
	_, _, err = index.TransportCapturedIndexLayouts(eval, journal, []index.LayoutCapture{{Lineage: index.LayoutLineage{Source: idx, Target: &targetIndex, SourceParent: parent, TargetParent: &targetParent}, Root: root}}, store, targetStore, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	layout := index.JournalIndexesOrZero(journal).UniqueRoots[idx.Address()]
	if layout == nil || layout.Gin != root.Gin || layout.Construction == root.Construction || !index.ConstructionMatches(layout, &targetIndex) {
		t.Fatal("layout rebuilt payload or lost history")
	}
	versions := append(slices.Clone(store.Versions[:1]), store.Versions[2:]...)
	compact := &storage.Relation{Versions: versions}
	vacuumed, err := storage.RemapVacuumIndex(context.Background(), root, store.Versions, storage.VacuumCandidate{Versions: versions, Positions: []int{0, -1, 1, 2, 3}, Removed: 1}, compact, uint64(e.Visibility.CommitSeq()))
	if err != nil {
		t.Fatal(err)
	}
	ginAssertOwners(t, vacuumed, compact)
	for col, tc := range networkPrivateCases {
		query := ginInput(t, vacuumed.Gin.Columns[col].Binding.Usage.Type, tc.boundary)
		got, err := index.GinScalarCandidates(context.Background(), vacuumed.Gin.Columns[col], query, 3, sqltype.InputEnvironment{})
		row := store.Versions[2]
		if err != nil || !slices.Equal(got, []storage.GinPosting{{Row: row.Row, Version: row.Version, Position: 1}}) {
			t.Fatal("vacuum exact slots", tc.typ, got, err)
		}
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	targetIdx, restored := ginInstalledRoot(t, target, "network_private_i")
	if restored == root || restored.Gin == root.Gin || targetIdx == idx {
		t.Fatal("replacement imported executable root")
	}
	for col := range networkPrivateCases {
		a, b := root.Gin.Columns[col], restored.Gin.Columns[col]
		if a.Binding.Same(b.Binding) || a.Binding.Class.Support(1).Authority == b.Binding.Class.Support(1).Authority || len(a.Tokens) != len(b.Tokens) || &a.Tokens[0] == &b.Tokens[0] {
			t.Fatal("replacement retained source authority/backing", col)
		}
		for slot := 1; slot <= 5; slot++ {
			if a.Binding.Class.Support(slot).Authority == b.Binding.Class.Support(slot).Authority {
				t.Fatal("source helper authority imported", col, slot)
			}
		}
		if _, err := routine.BindClassSupport(target.CatalogRead(), a.Binding.Class); err == nil {
			t.Fatal("destination admitted source capture")
		}
		for _, list := range b.Tokens {
			if list.Token.Kind == sqltype.GinOrdinary && list.Token.Element.Type() != b.Binding.Usage.Type {
				t.Fatal("destination token generation")
			}
		}
	}
	for _, q := range []string{"ALTER TABLE network_private ADD COLUMN padding integer", "REINDEX INDEX network_private_i", "DELETE FROM network_private WHERE id=2", "VACUUM network_private"} {
		enginetest.Run(t, target, q)
		current, next := ginInstalledRoot(t, target, "network_private_i")
		if len(next.Gin.Columns) != 6 || !index.ConstructionMatches(next, current) {
			t.Fatal("replacement transport", q)
		}
	}
	if !reflect.DeepEqual(root.Gin, before) || !reflect.DeepEqual(root.Entries, entries) {
		t.Fatal("transport/replacement mutated source")
	}
	t.Run("empty_replacement", func(t *testing.T) {
		source, _, empty := networkEngine(t, false)
		var text bytes.Buffer
		if err := engine.Dump(context.Background(), source, &text); err != nil {
			t.Fatal(err)
		}
		dest := engine.New()
		if err := engine.Restore(context.Background(), dest, &text); err != nil {
			t.Fatal(err)
		}
		_, next := ginInstalledRoot(t, dest, "network_private_i")
		if len(next.Entries) != 0 || len(next.Gin.Columns) != 6 || next.Gin.Columns[0].Binding.Same(empty.Gin.Columns[0].Binding) {
			t.Fatal("empty replacement lost destination bindings")
		}
	})
}
