package util

import (
	"hash/maphash"
	"math/bits"
	"slices"
)

// The trie consumes the 64-bit key hash five bits per level, lowest bits
// first. Levels at shift 0, 5, ..., 60 are bitmap nodes; the level at shift 60
// uses the last four bits. Keys whose whole hashes are equal meet in a
// collision node at shift 65, which keeps its entries in a plain list.
const (
	pmapLevelBits      = 5
	pmapLevelMask      = 1<<pmapLevelBits - 1
	pmapCollisionShift = 65
)

// pmapSeed is chosen once per process, so iteration order is stable for one
// version within a process but never across processes.
var pmapSeed = maphash.MakeSeed()

func pmapHash[K comparable](k K) uint64 { return maphash.Comparable(pmapSeed, k) }

type pmapEntry[K comparable, V any] struct {
	hash  uint64
	key   K
	value V
}

// edit identifies one Transient. A node whose edit is that Transient's may be
// changed in place by it; every other node is shared and is copied first.
// The field gives each token its own address: zero-size allocations may all
// share one.
type pmapEdit struct{ _ byte }

// node is a CHAMP bitmap node. Bit i of dataMap marks an entry whose fragment
// at this level is i, and bit i of nodeMap marks a child. entries and children
// are ordered by fragment. A collision node uses neither bitmap.
//
// The trie is canonical: no node other than the root holds exactly one entry
// and no children, so every entry sits at the shallowest level where its hash
// prefix is unique. Inserts and deletes keep that shape, and From builds it.
type pmapNode[K comparable, V any] struct {
	edit     *pmapEdit
	dataMap  uint32
	nodeMap  uint32
	entries  []pmapEntry[K, V]
	children []*pmapNode[K, V]
}

func pmapBit(h uint64, shift uint) uint32 { return 1 << ((h >> shift) & pmapLevelMask) }

func pmapIndex(bitmap, bit uint32) int { return bits.OnesCount32(bitmap & (bit - 1)) }

func (n *pmapNode[K, V]) singleEntry() bool { return len(n.entries) == 1 && len(n.children) == 0 }

// mutable returns a node that the edit may change and whether its slices may
// be changed in place.
//
// A Transient (e != nil) owns the result: n itself when the Transient created
// it, otherwise a copy with its own slices, because the Transient goes on to
// edit that copy in place. A persistent edit (e == nil) gets a copy that
// shares n's slices; the helpers below replace a shared slice instead of
// writing to it, and such a copy is never edited in place.
func (n *pmapNode[K, V]) mutable(e *pmapEdit) (*pmapNode[K, V], bool) {
	if e != nil && n.edit == e {
		return n, true
	}
	c := &pmapNode[K, V]{edit: e, dataMap: n.dataMap, nodeMap: n.nodeMap, entries: n.entries, children: n.children}
	if e == nil {
		return c, false
	}
	c.entries, c.children = slices.Clone(n.entries), slices.Clone(n.children)
	return c, true
}

func pmapInsert[T any](s []T, i int, v T, inPlace bool) []T {
	if inPlace {
		return slices.Insert(s, i, v)
	}
	out := make([]T, len(s)+1)
	copy(out, s[:i])
	out[i] = v
	copy(out[i+1:], s[i:])
	return out
}

func pmapRemove[T any](s []T, i int, inPlace bool) []T {
	if inPlace {
		return slices.Delete(s, i, i+1)
	}
	if len(s) == 1 {
		return nil
	}
	out := make([]T, len(s)-1)
	copy(out, s[:i])
	copy(out[i:], s[i+1:])
	return out
}

func pmapReplace[T any](s []T, i int, v T, inPlace bool) []T {
	if !inPlace {
		s = slices.Clone(s)
	}
	s[i] = v
	return s
}

func (n *pmapNode[K, V]) get(h uint64, k K) (V, bool) {
	for shift := uint(0); ; shift += pmapLevelBits {
		if shift >= pmapCollisionShift {
			for i := range n.entries {
				if n.entries[i].key == k {
					return n.entries[i].value, true
				}
			}
			break
		}
		bit := pmapBit(h, shift)
		if n.dataMap&bit != 0 {
			e := &n.entries[pmapIndex(n.dataMap, bit)]
			if e.hash == h && e.key == k {
				return e.value, true
			}
			break
		}
		if n.nodeMap&bit == 0 {
			break
		}
		n = n.children[pmapIndex(n.nodeMap, bit)]
	}
	var zero V
	return zero, false
}

// set stores ent below n, which sits at shift, and reports whether the key is
// new. It returns n itself only when it changed n in place.
func (n *pmapNode[K, V]) set(e *pmapEdit, shift uint, ent pmapEntry[K, V]) (*pmapNode[K, V], bool) {
	if shift >= pmapCollisionShift {
		m, inPlace := n.mutable(e)
		for i := range n.entries {
			if n.entries[i].key == ent.key {
				m.entries = pmapReplace(m.entries, i, ent, inPlace)
				return m, false
			}
		}
		m.entries = pmapInsert(m.entries, len(m.entries), ent, inPlace)
		return m, true
	}
	bit := pmapBit(ent.hash, shift)
	if n.dataMap&bit != 0 {
		i := pmapIndex(n.dataMap, bit)
		current := n.entries[i]
		m, inPlace := n.mutable(e)
		if current.hash == ent.hash && current.key == ent.key {
			m.entries = pmapReplace(m.entries, i, ent, inPlace)
			return m, false
		}
		child := pmapPair(e, shift+pmapLevelBits, current, ent)
		m.entries = pmapRemove(m.entries, i, inPlace)
		m.dataMap &^= bit
		m.nodeMap |= bit
		m.children = pmapInsert(m.children, pmapIndex(m.nodeMap, bit), child, inPlace)
		return m, true
	}
	if n.nodeMap&bit != 0 {
		j := pmapIndex(n.nodeMap, bit)
		child, added := n.children[j].set(e, shift+pmapLevelBits, ent)
		m, inPlace := n.mutable(e)
		if child != n.children[j] {
			m.children = pmapReplace(m.children, j, child, inPlace)
		}
		return m, added
	}
	m, inPlace := n.mutable(e)
	m.dataMap |= bit
	m.entries = pmapInsert(m.entries, pmapIndex(m.dataMap, bit), ent, inPlace)
	return m, true
}

// pmapPair returns the smallest subtrie at shift holding two distinct keys.
func pmapPair[K comparable, V any](e *pmapEdit, shift uint, a, b pmapEntry[K, V]) *pmapNode[K, V] {
	if shift >= pmapCollisionShift {
		return &pmapNode[K, V]{edit: e, entries: []pmapEntry[K, V]{a, b}}
	}
	abit, bbit := pmapBit(a.hash, shift), pmapBit(b.hash, shift)
	if abit == bbit {
		return &pmapNode[K, V]{edit: e, nodeMap: abit, children: []*pmapNode[K, V]{pmapPair(e, shift+pmapLevelBits, a, b)}}
	}
	if bbit < abit {
		a, b = b, a
	}
	return &pmapNode[K, V]{edit: e, dataMap: abit | bbit, entries: []pmapEntry[K, V]{a, b}}
}

// delete removes the key below n, which sits at shift, and reports whether it
// was present. A child left holding a single entry is inlined into n, so the
// trie stays canonical. It returns n unchanged when the key is absent.
func (n *pmapNode[K, V]) delete(e *pmapEdit, shift uint, h uint64, k K) (*pmapNode[K, V], bool) {
	if shift >= pmapCollisionShift {
		for i := range n.entries {
			if n.entries[i].key == k {
				m, inPlace := n.mutable(e)
				m.entries = pmapRemove(m.entries, i, inPlace)
				return m, true
			}
		}
		return n, false
	}
	bit := pmapBit(h, shift)
	if n.dataMap&bit != 0 {
		i := pmapIndex(n.dataMap, bit)
		if n.entries[i].hash != h || n.entries[i].key != k {
			return n, false
		}
		m, inPlace := n.mutable(e)
		m.entries = pmapRemove(m.entries, i, inPlace)
		m.dataMap &^= bit
		return m, true
	}
	if n.nodeMap&bit == 0 {
		return n, false
	}
	j := pmapIndex(n.nodeMap, bit)
	child, removed := n.children[j].delete(e, shift+pmapLevelBits, h, k)
	if !removed {
		return n, false
	}
	m, inPlace := n.mutable(e)
	if child.singleEntry() {
		m.children = pmapRemove(m.children, j, inPlace)
		m.nodeMap &^= bit
		m.dataMap |= bit
		m.entries = pmapInsert(m.entries, pmapIndex(m.dataMap, bit), child.entries[0], inPlace)
		return m, true
	}
	if child != n.children[j] {
		m.children = pmapReplace(m.children, j, child, inPlace)
	}
	return m, true
}

func (n *pmapNode[K, V]) each(yield func(K, V) bool) bool {
	for i := range n.entries {
		if !yield(n.entries[i].key, n.entries[i].value) {
			return false
		}
	}
	for _, child := range n.children {
		if !child.each(yield) {
			return false
		}
	}
	return true
}

func pmapPopCount(bitmap uint32) int { return bits.OnesCount32(bitmap) }
