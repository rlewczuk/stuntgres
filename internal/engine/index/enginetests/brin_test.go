package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

type brinFixture struct {
	evaluation         *exec.Context
	parent, descriptor *catalog.Relation
	store              *storage.Relation
	typ                *sqltype.Descriptor
}

func brinInteger(t *testing.T, typ *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	v, err := sqltype.Integer(typ, n)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func newBrinFixture(t *testing.T, width int) brinFixture {
	t.Helper()
	e := engine.New()
	view := e.CatalogRead()
	typ, _ := view.LookupType(sqltype.Int4)
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90000}, Attributes: []int32{1, 2}, Columns: []catalog.Column{{Name: "n", Usage: typ.Usage()}, {Name: "other", Usage: typ.Usage()}}}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}, StorageOptions: catalog.RelationOptions{Base: []catalog.RelationOption{{Name: "pages_per_range", Kind: catalog.RelationOptionInteger, Integer: int64(width), Text: "2"}}}, SQLIndex: &catalog.IndexDefinition{Method: catalog.BrinMethod, MethodOID: 3580, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1}, {Attribute: 2}}, Ready: true, Valid: true, Live: true}}
	f := brinFixture{evaluation: &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent: parent, descriptor: d, store: &storage.Relation{Generation: 1}, typ: typ}
	for i, values := range [][2]int64{{1, 50}, {9, -1}, {4, 20}, {0, 9}, {7, 0}} {
		row := storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Born: 1, Values: []sqltype.Value{brinInteger(t, typ, values[0]), brinInteger(t, typ, values[1])}}
		if i == 3 {
			row.Values[0] = sqltype.Null(typ)
		}
		f.store.Versions = append(f.store.Versions, row)
	}
	return f
}

func (f brinFixture) build(t *testing.T) *storage.IndexRoot {
	t.Helper()
	r, err := index.BuildBrinIndexRoot(f.evaluation, f.parent, f.descriptor, f.store, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func brinBounds(t *testing.T, s storage.BrinSummary) (int64, int64) {
	t.Helper()
	low, err := s.Min.BaseValue()
	if err != nil {
		t.Fatal(err)
	}
	high, err := s.Max.BaseValue()
	if err != nil {
		t.Fatal(err)
	}
	a, err := low.Integer()
	if err != nil {
		t.Fatal(err)
	}
	b, err := high.Integer()
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestBrinIndexSummaryBuild(t *testing.T) {
	f := newBrinFixture(t, 2)
	r := f.build(t)
	if r.Hash != nil || len(r.Entries) != 0 || r.Brin == nil || r.Brin.EffectivePagesPerRange != 2 || len(r.Brin.Ranges) != 3 || r.Brin.Storage != f.store.IndexIdentity() || r.Brin.Prefix != 5 {
		t.Fatal("not a real slot-range root", r)
	}
	for i, want := range [][4]int64{{1, 9, -1, 50}, {4, 4, 9, 20}, {7, 7, 0, 0}} {
		s := r.Brin.Ranges[i]
		if !s.Summarized || len(s.Keys) != 2 {
			t.Fatal(s)
		}
		a, b := brinBounds(t, s.Keys[0])
		c, d := brinBounds(t, s.Keys[1])
		if [4]int64{a, b, c, d} != want || !s.Keys[0].HasNonNull || s.Keys[0].HasNull != (i == 1) {
			t.Fatal("independent per-key extrema", i, s, want)
		}
	}
	f.store = &storage.Relation{}
	if root := f.build(t); len(root.Brin.Ranges) != 0 {
		t.Fatal("empty build", root)
	}
	f.store.Versions = []storage.RowVersion{{Row: 1, Version: 1, Values: []sqltype.Value{sqltype.Null(f.typ), sqltype.Null(f.typ)}}}
	if s := f.build(t).Brin.Ranges[0].Keys[0]; !s.HasNull || s.HasNonNull || s.Min.Type() != nil || s.Max.Type() != nil {
		t.Fatal("all NULL", s)
	}
	class, _ := f.evaluation.Catalog.IndexClass(10130) // Released inet inclusion is real, not minmax.
	f.descriptor.SQLIndex.Keys[0].Class = class
	f.store = &storage.Relation{}
	if _, err := index.BuildBrinIndexRoot(f.evaluation, f.parent, f.descriptor, f.store, true, nil); err == nil {
		t.Fatal("empty build bypassed provider validation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f = newBrinFixture(t, 2)
	f.evaluation.Context = ctx
	if _, err := index.BuildBrinIndexRoot(f.evaluation, f.parent, f.descriptor, f.store, true, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBrinIndexConsistency(t *testing.T) {
	f := newBrinFixture(t, 2)
	root := f.build(t).Brin
	for _, tc := range []struct {
		strategy string
		query    int64
		ranges   []int
	}{
		{"<", 1, nil}, {"<=", 1, []int{0}}, {"=", 1, []int{0}},
		{"<", 4, []int{0}}, {"<=", 4, []int{0, 1}}, {"=", 4, []int{0, 1}},
		{">=", 4, []int{0, 1, 2}}, {">", 4, []int{0, 2}}, {"=", 5, []int{0}},
		{"=", 7, []int{0, 2}}, {">", 9, nil}, {">=", 9, []int{0}},
	} {
		var got []int
		var direct, rechecked []int
		for ordinal := range root.Ranges {
			candidate, err := index.BrinConsistent(context.Background(), root, ordinal, 0, tc.strategy, brinInteger(t, f.typ, tc.query), sqltype.InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			if candidate {
				got = append(got, ordinal)
			}
			for slot := ordinal * 2; slot < min(ordinal*2+2, len(f.store.Versions)); slot++ {
				v := f.store.Versions[slot].Values[0]
				if v.IsNull() {
					continue
				}
				n, err := v.Integer()
				if err != nil {
					t.Fatal(err)
				}
				matches := tc.strategy == "<" && n < tc.query || tc.strategy == "<=" && n <= tc.query || tc.strategy == "=" && n == tc.query || tc.strategy == ">=" && n >= tc.query || tc.strategy == ">" && n > tc.query
				if matches {
					direct = append(direct, slot)
					if candidate {
						rechecked = append(rechecked, slot)
					}
				}
			}
		}
		if !reflect.DeepEqual(got, tc.ranges) || !reflect.DeepEqual(direct, rechecked) {
			t.Fatal(tc, got, direct, rechecked)
		}
	}
	for _, tc := range []struct {
		ordinal  int
		strategy string
		want     bool
	}{{0, "IS NULL", false}, {1, "IS NULL", true}, {1, "IS NOT NULL", true}} {
		got, err := index.BrinConsistent(context.Background(), root, tc.ordinal, 0, tc.strategy, sqltype.Value{}, sqltype.InputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for ordinal := range root.Ranges {
		left, err := index.BrinConsistent(context.Background(), root, ordinal, 0, ">=", brinInteger(t, f.typ, 4), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		right, err := index.BrinConsistent(context.Background(), root, ordinal, 1, ">", brinInteger(t, f.typ, 30), sqltype.InputEnvironment{})
		if err != nil || (left && right) != (ordinal == 0) {
			t.Fatal("independent multi-key conjunction", ordinal, left, right, err)
		}
		// The sole candidate is deliberately lossy: the row with other=50
		// has n=1, and the row with n=9 has other=-1.
		for slot := ordinal * 2; slot < min(ordinal*2+2, len(f.store.Versions)); slot++ {
			row := f.store.Versions[slot]
			if row.Values[0].IsNull() {
				continue
			}
			n, err := row.Values[0].Integer()
			if err != nil {
				t.Fatal(err)
			}
			other, err := row.Values[1].Integer()
			if err != nil {
				t.Fatal(err)
			}
			if n >= 4 && other > 30 {
				t.Fatal("independently empty conjunction unexpectedly matched", slot)
			}
		}
	}
	root.Ranges[1].Summarized = false
	if got, err := index.BrinConsistent(context.Background(), root, 1, 0, "=", brinInteger(t, f.typ, 999), sqltype.InputEnvironment{}); err != nil || !got {
		t.Fatal("unsummarized false negative", got, err)
	}
	if got, err := index.BrinConsistent(context.Background(), root, 0, 0, "<>", brinInteger(t, f.typ, 4), sqltype.InputEnvironment{}); err != nil || !got {
		t.Fatal("nonmember must not prune", got, err)
	}
}

func (f brinFixture) merge(t *testing.T, old *storage.IndexRoot, writes []storage.RowVersion, nextLength int) *storage.IndexRoot {
	t.Helper()
	p := &index.RowPublication{Store: f.store, Root: f.store.Versions, Next: make([]storage.RowVersion, nextLength), Writes: writes, Generation: f.store.Generation, SingleSlot: -1}
	r, err := index.MergeBrinIndexRoot(f.evaluation, f.parent, f.descriptor, old, p, false, true)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBrinIndexSummaryMaintenance(t *testing.T) {
	for _, auto := range []bool{false, true} {
		f := newBrinFixture(t, 2)
		f.store.Versions = f.store.Versions[:2]
		f.descriptor.StorageOptions.Base = append(f.descriptor.StorageOptions.Base, catalog.RelationOption{Name: "autosummarize", Kind: catalog.RelationOptionBoolean, Boolean: auto})
		old := f.build(t)
		writes := []storage.RowVersion{{Row: 3, Version: 3, Values: []sqltype.Value{brinInteger(t, f.typ, 4), brinInteger(t, f.typ, 20)}}, {Row: 4, Version: 4, Values: []sqltype.Value{sqltype.Null(f.typ), brinInteger(t, f.typ, 9)}}, {Row: 5, Version: 5, Values: []sqltype.Value{brinInteger(t, f.typ, 7), brinInteger(t, f.typ, 0)}}}
		next := f.merge(t, old, writes, 5)
		if next.Brin.Ranges[1].Summarized != auto || next.Brin.Ranges[2].Summarized || !next.Brin.Ranges[0].Summarized || len(old.Brin.Ranges) != 1 {
			t.Fatal("autosummarize previous occupied range", auto, next, old)
		}
		if auto {
			a, b := brinBounds(t, next.Brin.Ranges[1].Keys[0])
			if a != 4 || b != 4 || !next.Brin.Ranges[1].Keys[0].HasNull {
				t.Fatal(next)
			}
		}
		p := &index.RowPublication{Store: f.store, Root: f.store.Versions, Next: make([]storage.RowVersion, 2), Writes: []storage.RowVersion{{Row: 1, Version: 6, Base: 1, Values: []sqltype.Value{brinInteger(t, f.typ, -5), brinInteger(t, f.typ, 100)}}}, SingleSlot: 0, Generation: 1}
		updated, err := index.MergeBrinIndexRoot(f.evaluation, f.parent, f.descriptor, old, p, false, true)
		if err != nil {
			t.Fatal(err)
		}
		a, b := brinBounds(t, updated.Brin.Ranges[0].Keys[0])
		if a != -5 || b != 9 {
			t.Fatal("same-slot UPDATE", updated)
		}
		a, b = brinBounds(t, old.Brin.Ranges[0].Keys[0])
		if a != 1 || b != 9 {
			t.Fatal("mutated captured root", old)
		}
		ends := &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{2: {Version: 2, End: 2}}}
		deleted, err := index.MergeBrinIndexRoot(f.evaluation, f.parent, f.descriptor, old, &index.RowPublication{Store: f.store, Root: f.store.Versions, Next: make([]storage.RowVersion, 2), NextEnds: ends, Generation: 1, SingleSlot: -1}, false, true)
		if err != nil {
			t.Fatal(err)
		}
		a, b = brinBounds(t, deleted.Brin.Ranges[0].Keys[0])
		if a != 1 || b != 9 || deleted.Brin.Ends != ends || old.Brin.Ends != nil {
			t.Fatal("DELETE lost conservative extrema or mutated ends", deleted, old)
		}
	}
}

func TestBrinIndexOptionsLifecycle(t *testing.T) {
	f := newBrinFixture(t, 2)
	root := f.build(t)
	f.descriptor.StorageOptions.Base[0].Integer = 1
	next := f.merge(t, root, nil, 5)
	if next.Brin.EffectivePagesPerRange != 2 || len(next.Brin.Ranges) != 3 {
		t.Fatal("ALTER resized effective ranges", next)
	}
	if rebuilt := f.build(t); rebuilt.Brin.EffectivePagesPerRange != 1 || len(rebuilt.Brin.Ranges) != 5 {
		t.Fatal("REINDEX ignored configured width", rebuilt)
	}
	f.descriptor.StorageOptions = catalog.RelationOptions{}
	if rebuilt := f.build(t); rebuilt.Brin.EffectivePagesPerRange != 128 || len(rebuilt.Brin.Ranges) != 1 {
		t.Fatal("RESET build default", rebuilt)
	}
}

func TestBrinIndexPublication(t *testing.T) {
	t.Run("real-publication", brinPublicationPipeline)
	f := newBrinFixture(t, 2)
	old := f.build(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			local := *f.evaluation
			p := &index.RowPublication{Store: f.store, Root: f.store.Versions, Next: make([]storage.RowVersion, 6), Writes: []storage.RowVersion{{Row: 2, Version: 6, Values: []sqltype.Value{brinInteger(t, f.typ, 99), brinInteger(t, f.typ, 0)}}}, Generation: 1, SingleSlot: -1}
			next, err := index.MergeBrinIndexRoot(&local, f.parent, f.descriptor, old, p, false, true)
			if err != nil {
				t.Error(err)
				return
			}
			if next.Brin == old.Brin || next.Brin.Storage != f.store || next.Brin.Generation != 2 || next.Brin.Prefix != 6 {
				t.Error("publication capture", next)
			}
			a, b := brinBounds(t, next.Brin.Ranges[2].Keys[0])
			if a != 7 || b != 99 {
				t.Error("appended successor used row ID instead of actual slot", a, b)
			}
		})
	}
	wg.Wait()
	a, b := brinBounds(t, old.Brin.Ranges[2].Keys[0])
	if a != 7 || b != 7 || old.Brin.Prefix != 5 {
		t.Fatal("concurrent mutation", old)
	}
}

func TestBrinIndexReplacement(t *testing.T) {
	t.Run("admitted-callable-partial-replacement", brinProducedReplacement)
	t.Run("same-id-domain-generation", brinProducedGeneration)
	f := newBrinFixture(t, 2)
	old := f.build(t)
	state := catalog.CloneState(f.evaluation.Catalog.State())
	state.SetDescriptor(f.parent.Address(), f.parent)
	state.SetDescriptor(f.descriptor.Address(), f.descriptor)
	view := f.evaluation.Catalog.WithState(state)
	source := index.SnapshotSource{Index: f.descriptor, Parent: f.parent, Store: *f.store, Root: old, Frontier: 1}
	snapshot := &index.Snapshot{Sources: map[ids.ObjectAddress]index.SnapshotSource{f.descriptor.Address(): source}}
	destination := *f.store
	destination.Generation = 8
	positions, err := index.BuildPositions(context.Background(), &destination)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := snapshot.TransportRows(context.Background(), view, map[ids.ObjectAddress]*storage.Relation{f.parent.Address(): &destination}, map[ids.ObjectAddress]map[index.RowIdentity]int{f.parent.Address(): positions}, true)
	if err != nil {
		t.Fatal(err)
	}
	next := roots[f.descriptor.Address()]
	if next.Brin == old.Brin || next.Brin.Storage != destination.IndexIdentity() || next.Brin.Generation != 8 || next.Brin.EffectivePagesPerRange != 2 {
		t.Fatal("destination storage identity", next)
	}
	a, b := brinBounds(t, old.Brin.Ranges[0].Keys[0])
	if a != 1 || b != 9 || old.Brin.Storage != f.store {
		t.Fatal("source changed", old)
	}
	positions[index.RowIdentity{Row: 1, Version: 1}] = 1
	if _, err := snapshot.TransportRows(context.Background(), view, map[ids.ObjectAddress]*storage.Relation{f.parent.Address(): &destination}, map[ids.ObjectAddress]map[index.RowIdentity]int{f.parent.Address(): positions}, true); err == nil {
		t.Fatal("range remapping reused summaries")
	}
}

func TestBrinIndexInternalCharProvider(t *testing.T) {
	f := newBrinFixture(t, 2)
	typ, _ := f.evaluation.Catalog.LookupType(sqltype.Char)
	f.parent.Columns[0].Usage = typ.Usage()
	f.store.Versions = f.store.Versions[:2]
	for i, text := range []string{"z", "a"} {
		v, err := typ.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		f.store.Versions[i].Values[0] = v
	}
	root := f.build(t)
	s := root.Brin.Ranges[0].Keys[0]
	low, err := typ.Output(context.Background(), s.Min)
	if err != nil {
		t.Fatal(err)
	}
	high, err := typ.Output(context.Background(), s.Max)
	if err != nil || low != "a" || high != "z" || root.Brin.Bindings[0].Class.OID() != 10094 {
		t.Fatal("char minmax provider", low, high, err)
	}
}

func TestBrinIndexVacuum(t *testing.T) {
	t.Run("produced-callable-partial-vacuum", brinProducedVacuum)
	f := newBrinFixture(t, 2)
	old := f.build(t)
	candidate := storage.VacuumCandidate{Versions: []storage.RowVersion{f.store.Versions[0], f.store.Versions[2], f.store.Versions[3], f.store.Versions[4]}, Positions: []int{0, -1, 1, 2, 3}, Removed: 1}
	next, err := index.RebuildBrinVacuumRoot(context.Background(), f.descriptor, old, f.store.Versions, candidate, f.store, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Brin.Ranges) != 2 || next.Brin.EffectivePagesPerRange != 2 {
		t.Fatal(next)
	}
	for ordinal, want := range [][2]int64{{1, 9}, {4, 7}} {
		a, b := brinBounds(t, next.Brin.Ranges[ordinal].Keys[0])
		if [2]int64{a, b} != want || !next.Brin.Ranges[ordinal].Keys[0].HasNull {
			t.Fatal("conservative union across actual destination slots", ordinal, next)
		}
	}
	a, b := brinBounds(t, old.Brin.Ranges[0].Keys[0])
	if a != 1 || b != 9 || len(old.Brin.Ranges) != 3 || old.Brin.Generation != 1 {
		t.Fatal("VACUUM mutated source", old)
	}
	f.descriptor.StorageOptions.Base[0].Integer = 1
	frozen, err := index.RebuildBrinVacuumRoot(context.Background(), f.descriptor, old, f.store.Versions, storage.VacuumCandidate{Versions: f.store.Versions}, f.store, 2)
	if err != nil || frozen.Brin.EffectivePagesPerRange != 2 || len(frozen.Brin.Ranges) != 3 {
		t.Fatal("freeze-only changed effective width", frozen, err)
	}
	resized, err := index.RebuildBrinVacuumRoot(context.Background(), f.descriptor, old, f.store.Versions, candidate, f.store, 2)
	if err != nil || resized.Brin.EffectivePagesPerRange != 1 || len(resized.Brin.Ranges) != 4 {
		t.Fatal("compaction ignored configured width", resized, err)
	}
	unsummarized := *old.Brin
	unsummarized.Ranges = append([]storage.BrinRange(nil), old.Brin.Ranges...)
	unsummarized.Ranges[1] = storage.BrinRange{}
	f.descriptor.StorageOptions.Base[0].Integer = 2
	unknown, err := index.RebuildBrinVacuumRoot(context.Background(), f.descriptor, &storage.IndexRoot{Brin: &unsummarized}, f.store.Versions, candidate, f.store, 2)
	if err != nil || unknown.Brin.Ranges[0].Summarized || unknown.Brin.Ranges[1].Summarized {
		t.Fatal("unsummarized contributor lost", unknown, err)
	}
	for slot, row := range candidate.Versions {
		if row.Values[0].IsNull() {
			continue
		}
		for _, root := range []*storage.BrinRoot{next.Brin, unknown.Brin} {
			hit, err := index.BrinConsistent(context.Background(), root, slot/root.EffectivePagesPerRange, 0, "=", row.Values[0], sqltype.InputEnvironment{})
			if err != nil || !hit {
				t.Fatal("compaction false negative", slot, hit, err)
			}
		}
	}
}
