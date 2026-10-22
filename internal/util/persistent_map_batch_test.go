package util

import (
	"iter"
	"maps"
	"slices"
	"testing"
)

func pairs(kv ...int) iter.Seq2[int, int] {
	return func(yield func(int, int) bool) {
		for i := 0; i+1 < len(kv); i += 2 {
			if !yield(kv[i], kv[i+1]) {
				return
			}
		}
	}
}

func rangeModel(from, to, value int) map[int]int {
	model := map[int]int{}
	for k := from; k < to; k++ {
		model[k] = value
	}
	return model
}

func TestPersistentMapFrom(t *testing.T) {
	checkModel(t, PersistentMapFrom(pairs()), realHash, map[int]int{}, 1)
	checkModel(t, PersistentMapFrom(pairs(1, 1)), realHash, map[int]int{1: 1}, 2)
	checkModel(t, PersistentMapFrom(pairs(1, 1, 2, 2, 1, 3, 2, 4, 1, 5)), realHash, map[int]int{1: 5, 2: 4}, 3)
	model := rangeModel(0, 10000, 7)
	built := PersistentMapFrom(maps.All(model))
	checkModel(t, built, realHash, model, -1, 10000)
	var incremental PersistentMap[int, int]
	for k := range 10000 {
		incremental = incremental.Set(k, 7)
	}
	if shape(built) != shape(incremental) {
		t.Fatal("From and Set built different canonical shapes")
	}
}

// TestPersistentMapFromSlabsStayIndependent edits versions derived from a
// slab-built map. Slab slices have exact capacity, so no edit may write into
// the original's arrays.
func TestPersistentMapFromSlabsStayIndependent(t *testing.T) {
	model := rangeModel(0, 3000, 1)
	built := PersistentMapFrom(maps.All(model))
	derived := built
	for k := range 3000 {
		switch k % 3 {
		case 0:
			derived = derived.Set(k, 2)
		case 1:
			derived = derived.Delete(k)
		default:
			derived = derived.Set(k+5000, 3)
		}
	}
	tr := built.Transient()
	for k := range 3000 {
		tr.Set(k, 4)
		tr.Set(k+9000, 5)
	}
	_ = tr.Persistent()
	checkModel(t, built, realHash, model)
}

func TestPersistentMapSetAllAndDeleteAll(t *testing.T) {
	base := PersistentMapFrom(maps.All(rangeModel(0, 1000, 0)))
	added := base.SetAll(pairs(5, 50, 2000, 1, 5, 55, 3000, 2))
	model := rangeModel(0, 1000, 0)
	model[5], model[2000], model[3000] = 55, 1, 2
	checkModel(t, added, realHash, model, 1000, 4000)
	removed := added.DeleteAll(slices.Values([]int{5, 6, 2000, 99999, 5}))
	delete(model, 5)
	delete(model, 6)
	delete(model, 2000)
	checkModel(t, removed, realHash, model, 5, 6, 2000)
	checkModel(t, base, realHash, rangeModel(0, 1000, 0))
	if got := base.SetAll(pairs()); shape(got) != shape(base) || got.Len() != base.Len() {
		t.Fatal("an empty SetAll changed the map")
	}
	if got := base.DeleteAll(slices.Values([]int{-1, -2})); got.root != base.root {
		t.Fatal("deleting only absent keys copied the map")
	}
	var empty PersistentMap[int, int]
	checkModel(t, empty.SetAll(pairs(1, 1, 1, 2)), realHash, map[int]int{1: 2})
	checkModel(t, empty.DeleteAll(slices.Values([]int{1})), realHash, map[int]int{})
}

// TestPersistentMapSetAllCopiesSharedNodesOnce applies a batch to a large map
// and counts the nodes it created: a batch copies each node on its paths once,
// where the same changes made one by one copy the root for every change.
func TestPersistentMapSetAllCopiesSharedNodesOnce(t *testing.T) {
	base := PersistentMapFrom(maps.All(rangeModel(0, 3000, 0)))
	var batch []int
	for k := range 100 {
		batch = append(batch, k, 1)
	}
	edited := base.SetAll(pairs(batch...))
	if fresh := countFreshNodes(edited, base); fresh > 100*3 {
		t.Fatalf("batch of 100 created %d nodes", fresh)
	}
	if edited.root.edit == nil {
		t.Fatal("batch result root was not created by the batch")
	}
	model := rangeModel(0, 3000, 0)
	for k := range 100 {
		model[k] = 1
	}
	checkModel(t, edited, realHash, model)
}

// countFreshNodes counts nodes of m that old does not share.
func countFreshNodes[K comparable, V any](m, old PersistentMap[K, V]) int {
	shared := map[*pmapNode[K, V]]bool{}
	var mark func(n *pmapNode[K, V])
	mark = func(n *pmapNode[K, V]) {
		shared[n] = true
		for _, c := range n.children {
			mark(c)
		}
	}
	if old.root != nil {
		mark(old.root)
	}
	fresh := 0
	var walk func(n *pmapNode[K, V])
	walk = func(n *pmapNode[K, V]) {
		if shared[n] {
			return
		}
		fresh++
		for _, c := range n.children {
			walk(c)
		}
	}
	if m.root != nil {
		walk(m.root)
	}
	return fresh
}
