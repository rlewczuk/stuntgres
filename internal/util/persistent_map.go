// Package util holds small general-purpose data structures with no
// dependencies beyond the standard library.
//
// PersistentMap is a persistent (immutable, versioned) hash map. A value is
// one version. Set and Delete return a new version that shares every unchanged
// part with the old one, so keeping or copying a version costs O(1) and an
// update costs O(log n). Versions never change after they are created, so any
// number of goroutines may read them without locks. PersistentMapFrom builds a
// map from many entries at once, SetAll and DeleteAll apply a batch of changes
// in one pass, and Transient edits many keys in place before freezing the
// result. See README.md for the contracts.
package util

import "iter"

// PersistentMap is an immutable hash map from K to V. The zero PersistentMap is empty and ready to
// use. Keys are compared with ==; a key that is not equal to itself, such as a
// NaN, can be stored but never found again, as in a builtin map.
//
// Keys are hashed with hash/maphash.Comparable. Hashing allocates nothing for
// keys made of numbers, strings and arrays or structs of them; keys holding
// other pointers escape to the heap.
type PersistentMap[K comparable, V any] struct {
	root *pmapNode[K, V]
	size int
}

// Len returns the number of entries.
func (m PersistentMap[K, V]) Len() int { return m.size }

// Get returns the value stored for k and whether k is present.
func (m PersistentMap[K, V]) Get(k K) (V, bool) {
	if m.root == nil {
		var zero V
		return zero, false
	}
	return m.root.get(pmapHash(k), k)
}

// Set returns a version in which k maps to v. m itself is unchanged.
func (m PersistentMap[K, V]) Set(k K, v V) PersistentMap[K, V] {
	return m.set(nil, pmapEntry[K, V]{hash: pmapHash(k), key: k, value: v})
}

// Delete returns a version without k. It returns m itself when k is absent.
func (m PersistentMap[K, V]) Delete(k K) PersistentMap[K, V] {
	return m.delete(nil, pmapHash(k), k)
}

// All iterates the entries of this version in an unspecified order that is
// stable for the version within one process. Changing a variable that holds
// the map during iteration does not affect the iteration.
func (m PersistentMap[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if m.root != nil {
			m.root.each(yield)
		}
	}
}

// SetAll returns a version with every entry of entries stored, later entries
// for the same key winning. Each node shared with m is copied at most once.
func (m PersistentMap[K, V]) SetAll(entries iter.Seq2[K, V]) PersistentMap[K, V] {
	if m.root == nil {
		return PersistentMapFrom(entries)
	}
	t := m.Transient()
	for k, v := range entries {
		t.Set(k, v)
	}
	return t.Persistent()
}

// DeleteAll returns a version without any of keys. Each node shared with m is
// copied at most once.
func (m PersistentMap[K, V]) DeleteAll(keys iter.Seq[K]) PersistentMap[K, V] {
	if m.root == nil {
		return m
	}
	t := m.Transient()
	for k := range keys {
		t.Delete(k)
	}
	return t.Persistent()
}

func (m PersistentMap[K, V]) set(e *pmapEdit, ent pmapEntry[K, V]) PersistentMap[K, V] {
	if m.root == nil {
		root := &pmapNode[K, V]{edit: e, dataMap: pmapBit(ent.hash, 0), entries: []pmapEntry[K, V]{ent}}
		return PersistentMap[K, V]{root: root, size: 1}
	}
	root, added := m.root.set(e, 0, ent)
	size := m.size
	if added {
		size++
	}
	return PersistentMap[K, V]{root: root, size: size}
}

func (m PersistentMap[K, V]) delete(e *pmapEdit, h uint64, k K) PersistentMap[K, V] {
	if m.root == nil {
		return m
	}
	root, removed := m.root.delete(e, 0, h, k)
	if !removed {
		return m
	}
	if len(root.entries) == 0 && len(root.children) == 0 {
		return PersistentMap[K, V]{}
	}
	return PersistentMap[K, V]{root: root, size: m.size - 1}
}
