package index

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
)

// RoutineRootInput is captured by the catalog owner under Engine.mu. Publication
// is an opaque completed mapper proof; roots and stores are actual committed or
// already-produced private artifacts, not reconstructed index entries.
type RoutineRootInput struct {
	Publication     *stored.ScalarRoutinePublication
	Target          catalog.Read
	Index           *catalog.Relation
	Committed       catalog.Read
	CommittedRoots  map[ids.ObjectAddress]*storage.IndexRoot
	CommittedStores map[ids.ObjectAddress]*storage.Relation
	Store           *storage.Relation
	Previous        RootCandidate
	PrivateRoot     *storage.IndexRoot
	Rebuilds        map[ids.ObjectAddress]*catalog.Relation
	// Exact incumbent journal observations authorize prior private metadata COW.
	OriginalIndex, OriginalParent catalog.Descriptor
}

type routineRootPins struct {
	owner                  *catalog.Owner
	epoch                  ids.CatalogEpoch
	address, parentAddress ids.ObjectAddress
	index, parent          catalog.Descriptor
	root                   *storage.IndexRoot
	store                  *storage.Relation
	generation             uint64
	ends                   *storage.DeletionRoot
	length                 int
}

type routineRootSource struct {
	pins      *routineRootPins
	candidate RootCandidate // Sealed without its routineSource pointer.
	source    *catalog.Relation
	previous  *routineRootSource
}

// PreserveRoutineRoot authenticates executable-only publication without changing
// strict key semantics. A real later builder replaces this candidate through the
// incumbent RetainIndexBuild/recording path and thereby supersedes the receipt.
func PreserveRoutineRoot(in RoutineRootInput) (RootCandidate, error) {
	source, completed, old, err := in.Publication.IndexSource(in.Index)
	if err != nil {
		return RootCandidate{}, err
	}
	if in.Target.State() == nil || in.Committed.State() == nil || source.Owner() != in.Target.Owner() || source.Owner() != in.Committed.Owner() || source.Epoch() != in.Target.Epoch() || source.Epoch() != in.Committed.Epoch() || in.Target.Get(in.Index.Address()) != in.Index || in.Rebuilds[in.Index.Address()] != nil {
		return RootCandidate{}, ids.ErrCatalogReplaced
	}
	parent, ok := source.Relation(old.SQLIndex.Parent)
	nextParent, nextOK := completed.Relation(in.Index.SQLIndex.Parent)
	if !ok || !nextOK || in.Target.Get(nextParent.Address()) != nextParent || !sameRoutineIndexShape(old, in.Index) || !SameRelationRowShape(parent, nextParent) || in.Store == nil {
		return RootCandidate{}, ids.ErrCatalogReplaced
	}
	var root *storage.IndexRoot
	var pins *routineRootPins
	var previous *routineRootSource
	if in.PrivateRoot != nil {
		if in.Previous.Root != in.PrivateRoot || !in.Previous.MatchesIndex(old, in.Store) || !in.Previous.routineSourceCurrent(in.Committed, in.CommittedRoots, in.CommittedStores) {
			return RootCandidate{}, ids.ErrCatalogReplaced
		}
		root = in.PrivateRoot
		previous = in.Previous.routineSource
		if in.Previous.routineSource != nil {
			pins = in.Previous.routineSource.pins
		}
	} else {
		current, exists := in.Committed.Relation(old.Address())
		currentParent, parentExists := in.Committed.Relation(parent.Address())
		store := in.CommittedStores[parent.Address()]
		indexCurrent := exists && (current == old || in.OriginalIndex == current && SameIndexKeySemantics(current, old))
		parentCurrent := parentExists && (currentParent == parent || in.OriginalParent == currentParent && SameRelationRowShape(currentParent, parent))
		if !indexCurrent || !parentCurrent || store == nil || in.Store.IndexIdentity() != store || store.Generation != in.Store.Generation || store.Ends != in.Store.Ends || len(store.Versions) != len(in.Store.Versions) {
			return RootCandidate{}, ids.ErrCatalogReplaced
		}
		root = in.CommittedRoots[old.Address()]
	}
	if root == nil {
		return RootCandidate{}, ids.ErrCatalogReplaced
	}
	if pins == nil {
		store := in.CommittedStores[parent.Address()]
		pins = &routineRootPins{owner: in.Committed.Owner(), epoch: in.Committed.Epoch(), address: old.Address(), parentAddress: parent.Address(), index: in.Committed.Get(old.Address()), parent: in.Committed.Get(parent.Address()), root: in.CommittedRoots[old.Address()], store: store}
		if store != nil {
			pins.generation, pins.ends, pins.length = store.Generation, store.Ends, len(store.Versions)
		}
	}
	candidate := RootCandidate{Index: in.Index, Root: root, Storage: in.Store.IndexIdentity(), Generation: in.Store.Generation, Ends: in.Store.Ends, Length: len(in.Store.Versions)}
	candidate.routineSource = &routineRootSource{pins: pins, candidate: candidate, source: old, previous: previous}
	return candidate, nil
}

// The mapper proof authenticates expression occurrences. This independent check
// preserves all physical declarations while allowing only their reminted trees.
func sameRoutineIndexShape(a, b *catalog.Relation) bool {
	if a == nil || b == nil || a.Address() != b.Address() || a.Namespace() != b.Namespace() || a.Owner != b.Owner || a.RelationKind != b.RelationKind || !SameRelationRowShape(a, b) || !slices.Equal(a.Attributes, b.Attributes) || a.SQLIndex == nil || b.SQLIndex == nil {
		return false
	}
	x, y := a.SQLIndex, b.SQLIndex
	if x.Ready != y.Ready || x.Valid != y.Valid || x.Live != y.Live || x.PartitionParent != y.PartitionParent || x.Constraint != y.Constraint || x.Clustered != y.Clustered || x.ReplicaIdentity != y.ReplicaIdentity || len(x.Keys) != len(y.Keys) || (x.Predicate == nil) != (y.Predicate == nil) {
		return false
	}
	physical := *y
	physical.Keys = x.Keys
	physical.Predicate = x.Predicate
	if !sameIndexDefinitionSemantics(x, &physical) {
		return false
	}
	for i, left := range x.Keys {
		right := y.Keys[i]
		derived := right.Derived
		right.Derived = left.Derived
		if left != right || (left.Derived == nil) != (derived == nil) {
			return false
		}
		if left.Derived != nil && (!slices.Equal(left.Derived.Attributes, derived.Attributes) || !slices.Equal(left.Derived.Dependencies, derived.Dependencies) || left.Derived.Definition.ResultUsage() != derived.Definition.ResultUsage() || left.Derived.Execution.ResultUsage() != derived.Execution.ResultUsage()) {
			return false
		}
	}
	if x.Predicate != nil && (!slices.Equal(x.Predicate.Attributes, y.Predicate.Attributes) || !slices.Equal(x.Predicate.Dependencies, y.Predicate.Dependencies) || x.Predicate.Definition.ResultUsage() != y.Predicate.Definition.ResultUsage() || x.Predicate.Execution.ResultUsage() != y.Predicate.Execution.ResultUsage()) {
		return false
	}
	return true
}

func (c RootCandidate) routineSourceCurrent(view catalog.Read, roots map[ids.ObjectAddress]*storage.IndexRoot, stores map[ids.ObjectAddress]*storage.Relation) bool {
	if c.routineSource == nil {
		return true
	}
	receipt := c.routineSource
	plain := c
	plain.routineSource = nil
	if plain != receipt.candidate || receipt.pins == nil || view.State() == nil {
		return false
	}
	p := receipt.pins
	if view.Owner() != p.owner || view.Epoch() != p.epoch || view.Get(p.address) != p.index || view.Get(p.parentAddress) != p.parent || roots[p.address] != p.root || stores[p.parentAddress] != p.store {
		return false
	}
	return p.store == nil || p.store.Generation == p.generation && p.store.Ends == p.ends && len(p.store.Versions) == p.length
}

// RoutineSourcesMatch is an additional source fence, not a replacement for the
// target/store candidate fence. Both capture and final installation call it.
func RoutineSourcesMatch(candidates map[ids.ObjectAddress]RootCandidate, view catalog.Read, roots map[ids.ObjectAddress]*storage.IndexRoot, stores map[ids.ObjectAddress]*storage.Relation) bool {
	for _, candidate := range candidates {
		if !candidate.routineSourceCurrent(view, roots, stores) {
			return false
		}
	}
	return true
}
