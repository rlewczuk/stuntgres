package engine

import "stuntgres/internal/engine/ids"

// storageStore owns the engine's physical data state: committed relation
// storage, sequence generators, published index roots and the row and
// version identity allocators. It has no mutex. Engine.mu guards every field,
// and mutating methods carry the Locked suffix.
//
// Engine holds the store by value and never replaces it whole; publication
// swaps its maps under Engine.mu together with the catalog state. Published
// storage and index roots are immutable and shared copy-on-write, so
// snapshots retain them by pointer and never copy row values. Generator
// state here is distinct from transactional sequence metadata in the
// catalog.
type storageStore struct {
	relations   map[ids.ObjectAddress]*relationStorage
	sequences   map[ids.ObjectAddress]*sequenceGenerator
	indexRoots  map[ids.ObjectAddress]*logicalIndexRoot
	nextRow     uint64
	nextVersion uint64
}
