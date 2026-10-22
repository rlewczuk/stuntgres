package util

import (
	"fmt"
	"math/bits"
	"strings"
	"testing"
)

// hashFunc is the hash a test assigns to its keys. Tests that force
// collisions use white-box operations with their own hash; the others use
// pmapHash.
type hashFunc[K comparable] func(K) uint64

func realHash[K comparable](k K) uint64 { return pmapHash(k) }

// forcedSet, forcedDelete and forcedGet run the internal operations with a
// test-chosen hash.
func forcedSet[K comparable, V any](m PersistentMap[K, V], h hashFunc[K], k K, v V) PersistentMap[K, V] {
	return m.set(nil, pmapEntry[K, V]{hash: h(k), key: k, value: v})
}

func forcedDelete[K comparable, V any](m PersistentMap[K, V], h hashFunc[K], k K) PersistentMap[K, V] {
	return m.delete(nil, h(k), k)
}

func forcedGet[K comparable, V any](m PersistentMap[K, V], h hashFunc[K], k K) (V, bool) {
	if m.root == nil {
		var zero V
		return zero, false
	}
	return m.root.get(h(k), k)
}

// checkTrie verifies the structural invariants: bitmaps agree with slice
// lengths and do not overlap, entries are ordered by fragment and agree with
// the hash prefix of their path, collision nodes hold at least two distinct
// keys of one hash, the trie is canonical below the root, and the size is
// right.
func checkTrie[K comparable, V any](t testing.TB, m PersistentMap[K, V], h hashFunc[K]) {
	t.Helper()
	if m.root == nil {
		if m.size != 0 {
			t.Fatalf("empty trie with size %d", m.size)
		}
		return
	}
	if len(m.root.entries) == 0 && len(m.root.children) == 0 {
		t.Fatal("empty root node")
	}
	if got := checkNode(t, m.root, h, 0, 0, true); got != m.size {
		t.Fatalf("trie holds %d entries, size says %d", got, m.size)
	}
}

func prefixMask(shift uint) uint64 {
	if shift >= 64 {
		return ^uint64(0)
	}
	return 1<<shift - 1
}

func checkNode[K comparable, V any](t testing.TB, n *pmapNode[K, V], h hashFunc[K], shift uint, prefix uint64, root bool) int {
	t.Helper()
	for _, e := range n.entries {
		if e.hash != h(e.key) {
			t.Fatalf("entry %v stores hash %x, key hashes to %x", e.key, e.hash, h(e.key))
		}
		if e.hash&prefixMask(shift) != prefix {
			t.Fatalf("entry %v hash %x is off its path prefix %x at shift %d", e.key, e.hash, prefix, shift)
		}
	}
	if shift >= pmapCollisionShift {
		if n.dataMap != 0 || n.nodeMap != 0 || len(n.children) != 0 || len(n.entries) < 2 {
			t.Fatalf("bad collision node: data %b nodes %b children %d entries %d", n.dataMap, n.nodeMap, len(n.children), len(n.entries))
		}
		seen := map[K]bool{}
		for _, e := range n.entries {
			if seen[e.key] {
				t.Fatalf("collision node repeats key %v", e.key)
			}
			seen[e.key] = true
		}
		return len(n.entries)
	}
	if bits.OnesCount32(n.dataMap) != len(n.entries) || bits.OnesCount32(n.nodeMap) != len(n.children) || n.dataMap&n.nodeMap != 0 {
		t.Fatalf("bitmaps data %b nodes %b disagree with %d entries and %d children", n.dataMap, n.nodeMap, len(n.entries), len(n.children))
	}
	if !root && (len(n.entries)+len(n.children) == 0 || n.singleEntry()) {
		t.Fatalf("non-canonical node at shift %d: %d entries, %d children", shift, len(n.entries), len(n.children))
	}
	count, i, j := 0, 0, 0
	for fragment := uint64(0); fragment <= pmapLevelMask; fragment++ {
		bit := uint32(1) << fragment
		switch {
		case n.dataMap&bit != 0:
			if got := pmapBit(n.entries[i].hash, shift); got != bit {
				t.Fatalf("entry %d sits in fragment %b, hash says %b", i, bit, got)
			}
			i++
			count++
		case n.nodeMap&bit != 0:
			count += checkNode(t, n.children[j], h, shift+pmapLevelBits, prefix|fragment<<shift, false)
			j++
		}
	}
	return count
}

// checkModel verifies that m holds exactly model: Len, Get of every model key
// and of some absent probes, and an All that yields every entry once.
func checkModel[K comparable, V comparable](t testing.TB, m PersistentMap[K, V], h hashFunc[K], model map[K]V, absent ...K) {
	t.Helper()
	if m.Len() != len(model) {
		t.Fatalf("Len %d, model %d", m.Len(), len(model))
	}
	for k, want := range model {
		if got, ok := forcedGet(m, h, k); !ok || got != want {
			t.Fatalf("Get(%v) = %v, %v; want %v", k, got, ok, want)
		}
	}
	for _, k := range absent {
		if _, inModel := model[k]; inModel {
			continue
		}
		if got, ok := forcedGet(m, h, k); ok {
			t.Fatalf("absent key %v found with %v", k, got)
		}
	}
	seen := make(map[K]bool, len(model))
	for k, v := range m.All() {
		if seen[k] {
			t.Fatalf("All yielded %v twice", k)
		}
		seen[k] = true
		if want, ok := model[k]; !ok || want != v {
			t.Fatalf("All yielded %v=%v; model has %v, %v", k, v, want, ok)
		}
	}
	if len(seen) != len(model) {
		t.Fatalf("All yielded %d entries, model %d", len(seen), len(model))
	}
	checkTrie(t, m, h)
}

// shape renders the trie structure for comparing canonical forms.
func shape[K comparable, V any](m PersistentMap[K, V]) string {
	var b strings.Builder
	var walk func(n *pmapNode[K, V])
	walk = func(n *pmapNode[K, V]) {
		fmt.Fprintf(&b, "(%b/%b", n.dataMap, n.nodeMap)
		for _, e := range n.entries {
			fmt.Fprintf(&b, " %v", e.key)
		}
		for _, c := range n.children {
			walk(c)
		}
		b.WriteString(")")
	}
	if m.root != nil {
		walk(m.root)
	}
	return b.String()
}
