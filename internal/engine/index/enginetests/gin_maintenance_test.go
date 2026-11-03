package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"testing"
)

func ginMaintenanceDelta(t *testing.T, f ginFixture, pending bool) (*storage.IndexRoot, *index.RowPublication) {
	t.Helper()
	row := storage.RowVersion{Row: 20, Version: 20, Values: f.store.Versions[0].Values, IndexOutputs: map[ids.ObjectAddress]storage.IndexOutput{f.descriptor.Address(): {Index: f.descriptor, Member: true, Keys: f.root.Entries[0].Keys, GinPending: pending}}}
	publication := &index.RowPublication{Writes: []storage.RowVersion{row}, Root: f.store.Versions, Store: f.store, SingleSlot: -1}
	root, err := index.MergeGinIndexRoot(f.evaluation, f.parent, f.descriptor, f.root, publication, false, true)
	if err != nil {
		t.Fatal(err)
	}
	return root, publication
}
func TestGINMaintenanceSplit(t *testing.T) {
	f := newGinFixture(t)
	if storage.GinPendingIncidences(f.root.Gin) != 0 {
		t.Fatal("bulk build has pending work")
	}
	before := ginCopyPayload(f.root.Gin)
	root, _ := ginMaintenanceDelta(t, f, true)
	if len(root.Entries) != 9 || len(root.Gin.Pending) != 2 || storage.GinPendingIncidences(root.Gin) != 4 {
		t.Fatal("not genuine deduplicated deferred work", root.Gin)
	}
	ginWantRows(t, ginLookup(t, root.Gin.Columns[0], sqltype.GinToken{Element: f.root.Entries[0].Keys[0].ArrayElement(0)}), 1, 5, 6, 7)
	ginWantRows(t, ginLookup(t, root.Gin.Pending[0], sqltype.GinToken{Element: f.root.Entries[0].Keys[0].ArrayElement(0)}), 20)
	pendingBefore := ginCopyPayload(root.Gin)
	cleaned, n, err := storage.CleanGin(context.Background(), root.Gin, sqltype.InputEnvironment{})
	if err != nil || n != 4 || len(cleaned.Pending) != 0 {
		t.Fatal("transfer", n, err)
	}
	ginWantRows(t, ginLookup(t, cleaned.Columns[1], sqltype.GinToken{Lexeme: "cat"}), 1, 2, 5, 20)
	again, zero, err := storage.CleanGin(context.Background(), cleaned, sqltype.InputEnvironment{})
	if err != nil || again != cleaned || zero != 0 {
		t.Fatal("empty cleanup allocated", zero, err)
	}
	off, _ := ginMaintenanceDelta(t, f, false)
	if storage.GinPendingIncidences(off.Gin) != 0 || len(ginLookup(t, off.Gin.Columns[1], sqltype.GinToken{Lexeme: "cat"})) != 4 {
		t.Fatal("off output did not route main")
	}
	if !reflect.DeepEqual(before, f.root.Gin) || !reflect.DeepEqual(pendingBefore, root.Gin) {
		t.Fatal("cleanup mutated retained payload")
	}
	// A one-row batch with 129 distinct keys differs from a one-row batch with
	// duplicate keys. The counter is token work, not membership or heap pages.
	var elements []string
	for i := 1; i <= 129; i++ {
		elements = append(elements, fmt.Sprint(i))
	}
	keys := []sqltype.Value{ginInput(t, f.parent.Columns[0].Usage.Type, "{"+strings.Join(elements, ",")+"}"), ginInput(t, f.parent.Columns[1].Usage.Type, "")}
	payload, err := storage.MergeGin(context.Background(), f.root.Gin, nil, []storage.IndexEntry{{Row: 21, Version: 21, Position: 8, Keys: keys}}, nil, sqltype.InputEnvironment{})
	if err != nil || storage.GinPendingIncidences(payload) != 130 {
		t.Fatal("technical count", err)
	}
	if storage.GinPageEstimate(4) != 1 || storage.GinPageEstimate(128) != 1 || storage.GinPageEstimate(129) != 2 || storage.GinPageEstimate(130) != 2 || storage.GinPageEstimate(0) != 0 {
		t.Fatal("128-incidence estimate")
	}
}
func TestGINMaintenanceCandidates(t *testing.T) {
	f := newGinFixture(t)
	root, _ := ginMaintenanceDelta(t, f, true)
	for _, tc := range []struct {
		query    string
		strategy int
		want     []ids.RowID
	}{
		{"{1,2}", 2, []ids.RowID{1, 5, 6, 7, 20}}, {"{2}", 1, []ids.RowID{1, 2, 4, 5, 6, 7, 20}}, {"{}", 2, []ids.RowID{1, 2, 3, 4, 5, 6, 7, 20}}, {"{NULL}", 2, nil}, {"{1}", 3, []ids.RowID{1, 2, 3, 4, 5, 6, 7, 20}},
	} {
		got, err := index.GinArrayCandidates(context.Background(), root.Gin, 0, ginInput(t, f.parent.Columns[0].Usage.Type, tc.query), tc.strategy, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		ginWantRows(t, got, tc.want...)
	}
	for _, tc := range []struct {
		prefix, all bool
		want        []ids.RowID
	}{{false, false, []ids.RowID{1, 2, 5, 20}}, {true, false, []ids.RowID{1, 2, 4, 5, 20}}, {false, true, []ids.RowID{1, 2, 3, 4, 5, 6, 7, 20}}} {
		got, err := index.GinLexemeCandidates(context.Background(), root.Gin, 1, "cat", tc.prefix, tc.all, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		ginWantRows(t, got, tc.want...)
	}
	ended := map[ids.VersionID]ids.RowID{1: 1, 20: 20}
	filtered, err := storage.MergeGin(context.Background(), root.Gin, nil, nil, ended, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if storage.GinPendingIncidences(filtered) != 0 {
		t.Fatal("ended deferred version survived")
	}
	got, err := index.GinLexemeCandidates(context.Background(), filtered, 1, "cat", false, false, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	ginWantRows(t, got, 2, 5)
	// Intersection stays inside a coherent placement before the portion union.
	// Equal row values never coalesce two exact row/version identities.
	cleaned, _, err := storage.CleanGin(context.Background(), root.Gin, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	before, err := index.GinArrayCandidates(context.Background(), root.Gin, 0, f.root.Entries[0].Keys[0], 2, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	after, err := index.GinArrayCandidates(context.Background(), cleaned, 0, f.root.Entries[0].Keys[0], 2, sqltype.InputEnvironment{})
	if err != nil || !slices.Equal(before, after) {
		t.Fatal("cleanup changed candidates", err)
	}
}
func TestGINMaintenanceValidation(t *testing.T) {
	f := newGinFixture(t)
	root, p := ginMaintenanceDelta(t, f, true)
	before := ginCopyPayload(root.Gin)
	for _, materialize := range []bool{false, true} {
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		evaluation := *f.evaluation
		evaluation.Context = canceled
		candidate, err := index.MergeGinIndexRoot(&evaluation, f.parent, f.descriptor, root, p, false, materialize)
		if candidate != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled merge", materialize, err)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if candidate, _, err := storage.CleanGin(canceled, root.Gin, sqltype.InputEnvironment{}); candidate != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled transfer", err)
	}
	small := func() int64 {
		_, bytes := util.AllocationsPerCall(3, func() {
			candidate, err := index.MergeGinIndexRoot(f.evaluation, f.parent, f.descriptor, root, p, false, false)
			if err != nil || candidate != nil {
				t.Fatal(err)
			}
		})
		return bytes
	}
	baseline := small()
	if baseline > 128*1024 {
		t.Fatal("constant-delta validation allocated installation-sized output", baseline)
	}
	bad := *p
	bad.Writes = slices.Clone(p.Writes)
	bad.Writes[0].IndexOutputs = maps.Clone(p.Writes[0].IndexOutputs)
	output := bad.Writes[0].IndexOutputs[f.descriptor.Address()]
	output.Keys = slices.Clone(output.Keys)
	output.Keys[0] = output.Keys[1]
	bad.Writes[0].IndexOutputs[f.descriptor.Address()] = output
	if candidate, err := index.MergeGinIndexRoot(f.evaluation, f.parent, f.descriptor, root, &bad, false, false); candidate != nil || err == nil {
		t.Fatal("validation skipped typed extraction")
	}
	if !reflect.DeepEqual(before, root.Gin) {
		t.Fatal("validation mutated predecessor")
	}
}
func TestGINMaintenanceTransport(t *testing.T) {
	f := newGinFixture(t)
	root, _ := ginMaintenanceDelta(t, f, true)
	before := ginCopyPayload(root.Gin)
	mapped, err := storage.TransportGin(context.Background(), root.Gin, func(p storage.GinPosting) (storage.GinPosting, bool, error) { p.Position++; return p, p.Row != 2, nil })
	if err != nil {
		t.Fatal(err)
	}
	if storage.GinPendingIncidences(mapped) != 4 || mapped.Pending[0].Tokens[0].Postings[0].Position != 9 || !reflect.DeepEqual(before, root.Gin) {
		t.Fatal("split structural transport")
	}
	j := session.NewJournal()
	index.RetainIndexBuild(j, f.descriptor, f.store, root)
	state := catalog.CloneState(f.view.State())
	state.SetDescriptor(f.parent.Address(), f.parent)
	state.SetDescriptor(f.descriptor.Address(), f.descriptor)
	view := f.view.WithState(state)
	preparation := &index.GinMaintenance{Context: context.Background(), Index: f.descriptor, Store: f.store, Input: sqltype.InputEnvironment{}, View: view, TargetView: view}
	authority := preparation.CaptureAuthority(index.GinMaintenanceAuthority{View: view, Index: f.descriptor, Parent: f.parent, Journal: j})
	if err := preparation.Authorize(authority); err != nil {
		t.Fatal(err)
	}
	detached := *f.descriptor
	badAuthority := authority
	badAuthority.Index = &detached
	if err := preparation.Authorize(badAuthority); err == nil {
		t.Fatal("detached descriptor acquired cleanup authority")
	}
	broken := catalog.CloneState(state)
	member, _ := view.FamilySupport(2745, 2277, 2277, 2)
	changed := *member
	changed.Function = 333
	changed.ObjectRevision++
	broken.SetDescriptor(changed.Address(), &changed)
	badAuthority = authority
	badAuthority.View = view.WithState(broken)
	if err := preparation.Authorize(badAuthority); err == nil {
		t.Fatal("incompatible callback acquired cleanup authority")
	}
	next, err := preparation.Journal(j)
	if err != nil {
		t.Fatal(err)
	}
	if next == j || storage.GinPendingIncidences(index.JournalIndexesOrZero(next).Candidates[f.descriptor.Address()].Root.Gin) != 0 || index.JournalIndexesOrZero(j).Candidates[f.descriptor.Address()].Root != root {
		t.Fatal("candidate COW transfer")
	}
	// Savepoint/statement aliases must rebase through their actual accepted owner.
	if rebased := index.ReconcileGinJournal(j, next); rebased == j || index.JournalIndexesOrZero(rebased).Candidates[f.descriptor.Address()].Root != index.JournalIndexesOrZero(next).Candidates[f.descriptor.Address()].Root {
		t.Fatal("stale journal alias")
	}
}
func TestGINMaintenanceVacuum(t *testing.T) {
	f := newGinFixture(t)
	root, _ := ginMaintenanceDelta(t, f, true)
	versions := append(slices.Clone(f.store.Versions), storage.RowVersion{Row: 20, Version: 20, Values: f.store.Versions[0].Values})
	positions := make([]int, len(versions))
	for i := range positions {
		positions[i] = i
	}
	positions[1] = -1
	for i := 2; i < len(positions); i++ {
		positions[i]--
	}
	compact := append(slices.Clone(versions[:1]), versions[2:]...)
	destination := &storage.Relation{Versions: compact}
	next, err := storage.RemapVacuumIndex(context.Background(), root, versions, storage.VacuumCandidate{Versions: compact, Positions: positions, Removed: 1}, destination, 2)
	if err != nil {
		t.Fatal(err)
	}
	if storage.GinPendingIncidences(next.Gin) != 4 || next.Gin.Pending[0].Tokens[0].Postings[0].Position != 7 || storage.GinPendingIncidences(root.Gin) != 4 {
		t.Fatal("vacuum lost deferred placement")
	}
	ginAssertOwners(t, next, destination)
}
