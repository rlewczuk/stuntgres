package enginetests_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func compoundInstallationAuthorityProof(t *testing.T, source *core.Engine, sourceIndex *catalog.Relation) {
	t.Helper()
	target, targetIndex, _ := compoundEngine(t, false)
	for column := range 4 {
		a, b := sourceIndex.SQLIndex.Keys[column].Class, targetIndex.SQLIndex.Keys[column].Class
		if a.OID() != b.OID() || a.FamilyOID() != b.FamilyOID() {
			t.Fatal("authority fixture lacks identical independent numeric allocation")
		}
		for slot := 1; slot <= 5; slot++ {
			old, current := a.Support(slot), b.Support(slot)
			if old.Member != current.Member || old.Routine != current.Routine || old.Revision != current.Revision || old.ClassRevision != current.ClassRevision || old.FamilyRevision != current.FamilyRevision || old.RoutineRevision != current.RoutineRevision {
				t.Fatal("authority fixture lacks identical numeric stamps", column, slot, old, current)
			}
			if old.Authority == nil || current.Authority == nil || old.Authority == current.Authority {
				t.Fatal("independent installations share opaque executable authority", column, slot)
			}
		}
		if _, err := routine.BindClassSupport(target.CatalogRead(), a); err == nil {
			t.Fatal("foreign installation accepted merely identical IDs/revisions", column)
		}
		if _, err := routine.BindClassSupport(source.CatalogRead(), b); err == nil {
			t.Fatal("reverse foreign installation accepted", column)
		}
		if current, err := routine.BindClassSupport(target.CatalogRead(), b); err != nil || current != b {
			t.Fatal("fresh destination binding rejected", column, err)
		}
	}
}

func compoundScalarTransportProof(t *testing.T, e *core.Engine, idx *catalog.Relation, root *storage.IndexRoot) {
	t.Helper()
	view := e.CatalogRead()
	parent := view.MustRelation(idx.SQLIndex.Parent)
	store := e.Storage.Relations[parent.Address()]
	before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	selected := []int{0, 4}
	baseline := &storage.Relation{}
	positions := make(map[index.RowIdentity]int)
	for i, position := range selected {
		row := store.Versions[position]
		positions[index.RowIdentity{Row: row.Row, Version: row.Version}] = i
		baseline.Versions = append(baseline.Versions, row)
	}
	build := &index.AlterIndexBuild{Index: idx, Root: root, Store: store}
	filtered, err := build.BaselineRoot(evaluation, idx, baseline, positions)
	if err != nil {
		t.Fatal(err)
	}
	ginAssertOwners(t, filtered, baseline)
	if len(filtered.Entries) != 2 || filtered.Construction != root.Construction {
		t.Fatal("scalar baseline lost membership/construction")
	}
	for column, text := range []string{"111", "1", "m", "0.01"} {
		token := ginInput(t, filtered.Gin.Columns[column].Binding.Usage.Type, text)
		got, err := index.GinScalarCandidates(context.Background(), filtered.Gin, column, token, 3, sqltype.InputEnvironment{})
		row := baseline.Versions[1]
		want := []storage.GinPosting{{Row: row.Row, Version: row.Version, Position: 1}}
		if err != nil || !slices.Equal(got, want) {
			t.Fatal("baseline scalar posting lost exact destination position", column, got, want, err)
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
	for column, text := range []string{"100", "00", "a", "0.01"} {
		query := ginInput(t, moved.Gin.Columns[column].Binding.Usage.Type, text)
		positions := []int{2, 3}
		if column == 1 {
			positions = []int{2}
		}
		if column == 3 {
			positions = []int{3, 4}
		}
		var want []storage.GinPosting
		for _, position := range positions {
			want = append(want, storage.GinPosting{Row: ids.RowID(1000 + position), Version: ids.VersionID(2000 + position), Position: position})
		}
		got, err := index.GinScalarCandidates(context.Background(), moved.Gin, column, query, 3, sqltype.InputEnvironment{})
		if err != nil || !slices.Equal(got, want) {
			t.Fatal("snapshot scalar remint lost row/version/position", column, got, want, err)
		}
	}
	if moved == root || moved.Gin == root.Gin || moved.Construction == root.Construction || !index.ConstructionMatches(moved, idx) {
		t.Fatal("snapshot retained source physical owner or laundered construction")
	}
	// A slot-only layout shares actual tokens/postings and binding generations;
	// it must not execute the source expressions to reconstruct the same values.
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
	_, _, err = index.TransportCapturedIndexLayouts(evaluation, journal, []index.LayoutCapture{{Lineage: index.LayoutLineage{Source: idx, Target: &targetIndex, SourceParent: parent, TargetParent: &targetParent}, Root: root}}, store, targetStore, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	layout := index.JournalIndexesOrZero(journal).UniqueRoots[idx.Address()]
	if layout == nil || layout.Gin != root.Gin || layout.Construction == root.Construction || !index.ConstructionMatches(layout, &targetIndex) {
		t.Fatal("slot-only scalar layout rebuilt payload or lost lineage")
	}
	// Structural vacuum remaps selected old positions and all scalar lanes.
	vacuumPositions := []int{0, -1, 1, 2, 3, 4, 5}
	versions := append(slices.Clone(store.Versions[:1]), store.Versions[2:]...)
	compact := &storage.Relation{Versions: versions}
	vacuumed, err := storage.RemapVacuumIndex(context.Background(), root, store.Versions, storage.VacuumCandidate{Versions: versions, Positions: vacuumPositions, Removed: 1}, compact, uint64(e.Visibility.CommitSeq()))
	if err != nil {
		t.Fatal(err)
	}
	ginAssertOwners(t, vacuumed, compact)
	query := ginInput(t, vacuumed.Gin.Columns[3].Binding.Usage.Type, "0.01")
	got, err := index.GinScalarCandidates(context.Background(), vacuumed.Gin, 3, query, 3, sqltype.InputEnvironment{})
	want := []storage.GinPosting{{Row: store.Versions[3].Row, Version: store.Versions[3].Version, Position: 2}, {Row: store.Versions[4].Row, Version: store.Versions[4].Version, Position: 3}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatal("vacuum scalar destination positions", got, want, err)
	}
	if !reflect.DeepEqual(root.Gin, before) || !reflect.DeepEqual(root.Entries, entries) {
		t.Fatal("transport mutated original retained scalar root")
	}
}
