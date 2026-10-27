package util

// PersistentMapValues returns a map with m's keys in which each key maps to
// f(key, value). It copies m's trie, hashes and keys instead of hashing and
// partitioning them again, laying nodes and slices out in a few large
// allocations like PersistentMapFrom, so it is the cheap way to build many
// maps over one key set. f is called once per entry; m itself is unchanged and
// shares nothing with the result.
func PersistentMapValues[K comparable, V, W any](m PersistentMap[K, V], f func(K, V) W) PersistentMap[K, W] {
	if m.root == nil {
		return PersistentMap[K, W]{}
	}
	b := pmapBuilder[K, W]{entries: make([]pmapEntry[K, W], m.size)}
	return PersistentMap[K, W]{root: pmapMapValues(&b, m.root, f), size: m.size}
}

// pmapMapValues copies n and its subtree with mapped values. The copies are
// persistent nodes (no edit) whose slices have exact capacity.
func pmapMapValues[K comparable, V, W any](b *pmapBuilder[K, W], n *pmapNode[K, V], f func(K, V) W) *pmapNode[K, W] {
	c := b.node()
	c.dataMap, c.nodeMap = n.dataMap, n.nodeMap
	c.entries = b.take(len(n.entries))
	for i, e := range n.entries {
		c.entries[i] = pmapEntry[K, W]{hash: e.hash, key: e.key, value: f(e.key, e.value)}
	}
	c.children = b.takeChildren(len(n.children))
	for i, child := range n.children {
		c.children[i] = pmapMapValues(b, child, f)
	}
	return c
}
