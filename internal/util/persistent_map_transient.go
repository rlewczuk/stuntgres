package util

import "iter"

// Transient is a single-goroutine editor for many changes to one Map. Nodes it
// creates are changed in place by later edits; nodes it shares with other
// versions are copied once, on first change. Persistent freezes the result.
//
// A Transient must not be used concurrently, must not be changed while it is
// being iterated, and must not be used after Persistent.
type PersistentMapTransient[K comparable, V any] struct {
	edit      *pmapEdit
	root      *pmapNode[K, V]
	size      int
	iterating int
}

// Transient returns an editor that starts from m. m itself never changes.
func (m PersistentMap[K, V]) Transient() *PersistentMapTransient[K, V] {
	return &PersistentMapTransient[K, V]{edit: &pmapEdit{}, root: m.root, size: m.size}
}

func (t *PersistentMapTransient[K, V]) live() {
	if t.edit == nil {
		panic("util: persistent map transient used after Persistent")
	}
}

func (t *PersistentMapTransient[K, V]) writable() {
	t.live()
	if t.iterating != 0 {
		panic("util: persistent map transient changed during iteration")
	}
}

func (t *PersistentMapTransient[K, V]) current() PersistentMap[K, V] {
	return PersistentMap[K, V]{root: t.root, size: t.size}
}

func (t *PersistentMapTransient[K, V]) install(m PersistentMap[K, V]) {
	t.root, t.size = m.root, m.size
}

// Len returns the number of entries.
func (t *PersistentMapTransient[K, V]) Len() int {
	t.live()
	return t.size
}

// Get returns the value stored for k and whether k is present.
func (t *PersistentMapTransient[K, V]) Get(k K) (V, bool) {
	t.live()
	return t.current().Get(k)
}

// Set stores v for k.
func (t *PersistentMapTransient[K, V]) Set(k K, v V) {
	t.writable()
	t.install(t.current().set(t.edit, pmapEntry[K, V]{hash: pmapHash(k), key: k, value: v}))
}

// Delete removes k when present.
func (t *PersistentMapTransient[K, V]) Delete(k K) {
	t.writable()
	t.install(t.current().delete(t.edit, pmapHash(k), k))
}

// All iterates the current entries. Set and Delete panic until the iteration
// ends.
func (t *PersistentMapTransient[K, V]) All() iter.Seq2[K, V] {
	t.live()
	return func(yield func(K, V) bool) {
		t.live()
		t.iterating++
		defer func() { t.iterating-- }()
		if t.root != nil {
			t.root.each(yield)
		}
	}
}

// Persistent returns the edited map and ends the Transient. No node of the
// returned map is ever changed again.
func (t *PersistentMapTransient[K, V]) Persistent() PersistentMap[K, V] {
	t.writable()
	m := t.current()
	t.edit, t.root, t.size = nil, nil, 0
	return m
}
