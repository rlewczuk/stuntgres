package session

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// FenceKind fixes where publication checks a fence. The root engine decides
// the order in which publication checks the kinds.
type FenceKind uint8

// The session fence kinds. The root engine declares its own kinds from
// FenceRootKinds on.
const (
	FenceStorageRead FenceKind = iota // StorageReadFence
	FenceIndexSet                     // IndexSetFence
	FenceNamespace                    // NamespaceFence
	FenceRootKinds
)

// Fence is one feature's commit precondition in the catalog journal. A fence
// is immutable once recorded; journal clones and savepoint marks share it.
// The root engine records concrete fences and recovers them with one checked
// assertion when it validates them.
type Fence interface {
	Kind() FenceKind
}

// CommittedFence is a fence over the committed catalog and storage alone.
// The session fences implement it.
type CommittedFence interface {
	Fence
	// Current reports whether the fence still holds against committed and
	// the committed storage relations. Caller holds Engine.mu.
	Current(committed *catalog.State, relations map[ids.ObjectAddress]*storage.Relation) error
}
