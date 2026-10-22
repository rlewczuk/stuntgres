package util

import (
	"math/rand/v2"
	"testing"
)

// Forced hashes reach the parts of the trie real hashes almost never do:
// whole-hash collisions and long shared prefixes.
var forcedHashes = map[string]hashFunc[int]{
	// Every key has the same hash: one chain down to a collision node.
	"constant": func(int) uint64 { return 0xfeedface12345678 },
	// Keys share all but the top four bits: chains to the last level.
	"top-bits": func(k int) uint64 { return uint64(k%16)<<60 | 0x0123456789abcde },
	// A few distinct hashes, each shared by several keys.
	"buckets": func(k int) uint64 { return uint64(k%5) * 0x9e3779b97f4a7c15 },
	// Keys differ only in the lowest bits: a wide, shallow trie.
	"low-bits": func(k int) uint64 { return uint64(k) },
}

func TestPersistentMapForcedHashes(t *testing.T) {
	for name, h := range forcedHashes {
		t.Run(name, func(t *testing.T) {
			rng := rand.New(rand.NewPCG(3, 4))
			var m PersistentMap[int, int]
			model := map[int]int{}
			for step := range 4000 {
				k := rng.IntN(60)
				if rng.IntN(3) != 0 {
					m = forcedSet(m, h, k, step)
					model[k] = step
				} else {
					m = forcedDelete(m, h, k)
					delete(model, k)
				}
				checkModel(t, m, h, model, 60, 61)
			}
			for k := range model {
				m = forcedDelete(m, h, k)
				delete(model, k)
				checkModel(t, m, h, model)
			}
			if m.root != nil {
				t.Fatal("deleting every key left a root")
			}
		})
	}
}

func TestPersistentMapCollisionNodeLifecycle(t *testing.T) {
	h := forcedHashes["constant"]
	var m PersistentMap[int, string]
	m = forcedSet(m, h, 1, "a")
	if m.root.singleEntry() == false {
		t.Fatal("one key should sit in the root")
	}
	m = forcedSet(m, h, 2, "b")
	m = forcedSet(m, h, 3, "c")
	m = forcedSet(m, h, 2, "B")
	checkModel(t, m, h, map[int]string{1: "a", 2: "B", 3: "c"}, 4)
	depth := 0
	for n := m.root; len(n.children) != 0; n = n.children[0] {
		depth++
	}
	if depth != 13 {
		t.Fatalf("collision node at depth %d, want 13", depth)
	}
	m = forcedDelete(m, h, 3)
	m = forcedDelete(m, h, 1)
	checkModel(t, m, h, map[int]string{2: "B"}, 1, 3)
	if !m.root.singleEntry() {
		t.Fatal("the last colliding key was not inlined back into the root")
	}
}

func TestPersistentMapForcedHashTransientAndBuild(t *testing.T) {
	for name, h := range forcedHashes {
		t.Run(name, func(t *testing.T) {
			model := map[int]int{}
			var es []pmapEntry[int, int]
			for k := range 50 {
				es = append(es, pmapEntry[int, int]{hash: h(k), key: k, value: k})
				model[k] = k
			}
			built := pmapBuild(es)
			checkModel(t, built, h, model, 50)
			var incremental PersistentMap[int, int]
			for k := range 50 {
				incremental = forcedSet(incremental, h, k, k)
			}
			if name != "constant" && name != "buckets" && shape(built) != shape(incremental) {
				t.Fatalf("built and incremental shapes differ:\n%s\n%s", shape(built), shape(incremental))
			}
			tr := built.Transient()
			for k := range 50 {
				if k%3 == 0 {
					tr.install(tr.current().delete(tr.edit, h(k), k))
					delete(model, k)
				} else {
					tr.install(tr.current().set(tr.edit, pmapEntry[int, int]{hash: h(k), key: k, value: -k}))
					model[k] = -k
				}
			}
			edited := tr.Persistent()
			checkModel(t, edited, h, model, 0, 3)
			original := map[int]int{}
			for k := range 50 {
				original[k] = k
			}
			checkModel(t, built, h, original)
		})
	}
}
