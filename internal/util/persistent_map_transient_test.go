package util

import (
	"maps"
	"testing"
)

func TestPersistentMapTransientEdits(t *testing.T) {
	source := PersistentMapFrom(maps.All(rangeModel(0, 2000, 0)))
	tr := source.Transient()
	model := rangeModel(0, 2000, 0)
	for k := range 2000 {
		if k%4 == 0 {
			tr.Delete(k)
			delete(model, k)
		} else {
			tr.Set(k, k)
			model[k] = k
		}
		tr.Set(k+10000, -k)
		model[k+10000] = -k
	}
	if tr.Len() != len(model) {
		t.Fatalf("transient Len %d, model %d", tr.Len(), len(model))
	}
	if v, ok := tr.Get(10005); !ok || v != -5 {
		t.Fatalf("transient Get = %v, %v", v, ok)
	}
	seen := 0
	for k, v := range tr.All() {
		if model[k] != v {
			t.Fatalf("transient All yielded %v=%v", k, v)
		}
		seen++
	}
	if seen != len(model) {
		t.Fatalf("transient All yielded %d entries", seen)
	}
	result := tr.Persistent()
	checkModel(t, result, realHash, model)
	checkModel(t, source, realHash, rangeModel(0, 2000, 0))
	// The frozen result is an ordinary version: later edits leave it alone.
	_ = result.Set(1, 99).Delete(2)
	again := result.Transient()
	again.Set(3, 99)
	again.Delete(5)
	_ = again.Persistent()
	checkModel(t, result, realHash, model)
}

func TestPersistentMapTransientEditsOwnNodesInPlace(t *testing.T) {
	tr := PersistentMapFrom(maps.All(rangeModel(0, 3000, 0))).Transient()
	tr.Set(42, 1)
	if allocs := testing.AllocsPerRun(100, func() { tr.Set(42, 2) }); allocs != 0 {
		t.Fatalf("re-setting a key on an owned path allocated %.0f times", allocs)
	}
	persistent := tr.Persistent()
	if allocs := testing.AllocsPerRun(100, func() { _ = persistent.Set(42, 3) }); allocs == 0 {
		t.Fatal("a persistent Set did not copy its path")
	}
}

func expectPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s did not panic", what)
		}
	}()
	f()
}

func TestPersistentMapTransientMisuse(t *testing.T) {
	tr := PersistentMap[int, int]{}.Set(1, 1).Set(2, 2).Transient()
	expectPanic(t, "Set during All", func() {
		for k := range tr.All() {
			tr.Set(k+10, 0)
		}
	})
	expectPanic(t, "Delete during All", func() {
		for k := range tr.All() {
			tr.Delete(k)
		}
	})
	tr.Set(3, 3)
	_ = tr.Persistent()
	for what, f := range map[string]func(){
		"Set":        func() { tr.Set(4, 4) },
		"Delete":     func() { tr.Delete(1) },
		"Get":        func() { tr.Get(1) },
		"Len":        func() { tr.Len() },
		"All":        func() { tr.All() },
		"Persistent": func() { tr.Persistent() },
	} {
		expectPanic(t, what+" after Persistent", f)
	}
}
