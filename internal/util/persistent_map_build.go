package util

import "iter"

// PersistentMapFrom builds a map holding entries, later entries for the same
// key winning. It hashes the entries once and partitions them level by level,
// the way the trie splits them, laying nodes and slices out in a few large
// allocations. It is the cheap way to create a large map. The result has the
// same canonical shape as one built by Set.
func PersistentMapFrom[K comparable, V any](entries iter.Seq2[K, V]) PersistentMap[K, V] {
	var es []pmapEntry[K, V]
	for k, v := range entries {
		es = append(es, pmapEntry[K, V]{hash: pmapHash(k), key: k, value: v})
	}
	return pmapBuild(es)
}

// PersistentMapOf builds a map holding the entries of m, like
// PersistentMapFrom(maps.All(m)) without growing its working arrays.
func PersistentMapOf[K comparable, V any](m map[K]V) PersistentMap[K, V] {
	es := make([]pmapEntry[K, V], 0, len(m))
	for k, v := range m {
		es = append(es, pmapEntry[K, V]{hash: pmapHash(k), key: k, value: v})
	}
	return pmapBuild(es)
}

// pmapSortKey is a pointer-free stand-in for one input entry: its hash and
// its input position.
type pmapSortKey struct {
	hash  uint64
	index int
}

// pmapArenaChunk is how many nodes or child pointers one arena allocation
// holds.
const pmapArenaChunk = 256

// pmapBuilder partitions sort keys and hands out nodes and exact-capacity
// slices from large arrays. Exact capacity makes any later change copy a slice
// instead of appending into a neighbour's part of an array.
type pmapBuilder[K comparable, V any] struct {
	source   []pmapEntry[K, V]
	entries  []pmapEntry[K, V]
	nodes    []pmapNode[K, V]
	children []*pmapNode[K, V]
	size     int
}

// pmapBuild lays es out as a canonical trie.
func pmapBuild[K comparable, V any](es []pmapEntry[K, V]) PersistentMap[K, V] {
	if len(es) == 0 {
		return PersistentMap[K, V]{}
	}
	keys := make([]pmapSortKey, 2*len(es))
	for i := range es {
		keys[i] = pmapSortKey{hash: es[i].hash, index: i}
	}
	b := pmapBuilder[K, V]{source: es, entries: make([]pmapEntry[K, V], len(es))}
	group := keys[:len(es)]
	if pmapSameHash(group) {
		group = pmapDedupe(group, es)
	}
	var root *pmapNode[K, V]
	if len(group) == 1 {
		root = b.node()
		root.dataMap = pmapBit(group[0].hash, 0)
		root.entries = b.take(1)
		root.entries[0] = es[group[0].index]
		b.size = 1
	} else {
		root = b.build(group, keys[len(es):], 0)
	}
	return PersistentMap[K, V]{root: root, size: b.size}
}

func pmapSameHash(keys []pmapSortKey) bool {
	for _, k := range keys[1:] {
		if k.hash != keys[0].hash {
			return false
		}
	}
	return true
}

// pmapDedupe keeps one key per entry key among keys of one hash, at the
// position of its first occurrence and pointing at its last, so later entries
// win.
func pmapDedupe[K comparable, V any](keys []pmapSortKey, es []pmapEntry[K, V]) []pmapSortKey {
	out := keys[:0]
	for _, k := range keys {
		duplicate := false
		for i := range out {
			if es[out[i].index].key == es[k.index].key {
				out[i].index = k.index
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, k)
		}
	}
	return out
}

func (b *pmapBuilder[K, V]) node() *pmapNode[K, V] {
	if len(b.nodes) == 0 {
		b.nodes = make([]pmapNode[K, V], pmapArenaChunk)
	}
	n := &b.nodes[0]
	b.nodes = b.nodes[1:]
	return n
}

func (b *pmapBuilder[K, V]) take(n int) []pmapEntry[K, V] {
	if n == 0 {
		return nil
	}
	out := b.entries[:n:n]
	b.entries = b.entries[n:]
	return out
}

func (b *pmapBuilder[K, V]) takeChildren(n int) []*pmapNode[K, V] {
	if n == 0 {
		return nil
	}
	if len(b.children) < n {
		b.children = make([]*pmapNode[K, V], max(n, pmapArenaChunk))
	}
	out := b.children[:n:n]
	b.children = b.children[n:]
	return out
}

// pmapSmallGroup is the largest group build sorts by insertion instead of by
// counting into all 32 fragments.
const pmapSmallGroup = 8

// pmapRun is the part [lo, hi) of a sorted group sharing fragment f.
type pmapRun struct {
	f      uint
	lo, hi int
}

// build returns the node at shift holding keys, at least two distinct keys.
// scratch has keys' length. Large groups are counting-sorted by fragment into
// scratch, small ones insertion-sorted in place; children recurse on the
// sorted runs with the other buffer as their scratch.
func (b *pmapBuilder[K, V]) build(keys, scratch []pmapSortKey, shift uint) *pmapNode[K, V] {
	n := b.node()
	if shift >= pmapCollisionShift {
		n.entries = b.take(len(keys))
		for i, k := range keys {
			n.entries[i] = b.source[k.index]
		}
		b.size += len(keys)
		return n
	}
	var runs [1 << pmapLevelBits]pmapRun
	sorted, other, count := pmapSortRuns(keys, scratch, shift, &runs)
	for i := range runs[:count] {
		r := &runs[i]
		if run := sorted[r.lo:r.hi]; len(run) > 1 && pmapSameHash(run) {
			r.hi = r.lo + len(pmapDedupe(run, b.source))
		}
		if r.hi-r.lo == 1 {
			n.dataMap |= 1 << r.f
		} else {
			n.nodeMap |= 1 << r.f
		}
	}
	n.entries = b.take(pmapPopCount(n.dataMap))
	n.children = b.takeChildren(pmapPopCount(n.nodeMap))
	inline, kids := 0, 0
	for _, r := range runs[:count] {
		if r.hi-r.lo == 1 {
			n.entries[inline] = b.source[sorted[r.lo].index]
			inline++
			b.size++
			continue
		}
		n.children[kids] = b.build(sorted[r.lo:r.hi], other[r.lo:r.hi], shift+pmapLevelBits)
		kids++
	}
	return n
}

// pmapSortRuns orders keys by fragment at shift, stably, and fills runs with
// the non-empty fragments in order. It returns the buffer holding the sorted
// keys, the other buffer, and the number of runs.
func pmapSortRuns(keys, scratch []pmapSortKey, shift uint, runs *[1 << pmapLevelBits]pmapRun) (sorted, other []pmapSortKey, count int) {
	fragment := func(k pmapSortKey) uint { return uint(k.hash >> shift & pmapLevelMask) }
	if len(keys) <= pmapSmallGroup {
		for i := 1; i < len(keys); i++ {
			for j := i; j > 0 && fragment(keys[j-1]) > fragment(keys[j]); j-- {
				keys[j-1], keys[j] = keys[j], keys[j-1]
			}
		}
		for i := 0; i < len(keys); {
			f := fragment(keys[i])
			j := i + 1
			for j < len(keys) && fragment(keys[j]) == f {
				j++
			}
			runs[count] = pmapRun{f: f, lo: i, hi: j}
			count++
			i = j
		}
		return keys, scratch, count
	}
	var start [1<<pmapLevelBits + 1]int
	for _, k := range keys {
		start[fragment(k)+1]++
	}
	for f := 1; f < len(start); f++ {
		start[f] += start[f-1]
	}
	next := start
	for _, k := range keys {
		f := fragment(k)
		scratch[next[f]] = k
		next[f]++
	}
	for f := range uint(1 << pmapLevelBits) {
		if start[f] < start[f+1] {
			runs[count] = pmapRun{f: f, lo: start[f], hi: start[f+1]}
			count++
		}
	}
	return scratch, keys, count
}
