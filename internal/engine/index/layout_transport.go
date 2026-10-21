package index

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// evolveIndexRevision records this only for its explicit slot-only path, with
// unchanged referenced Usage. Pointer equality of remapped ASTs is not a proof.
type LayoutLineage struct {
	Source, Target             *catalog.Relation
	SourceParent, TargetParent *catalog.Relation
	Previous                   *LayoutLineage
}

func LayoutInputsUnchanged(index, oldParent, parent *catalog.Relation) bool {
	if index == nil || oldParent == nil || parent == nil || oldParent.Address() != parent.Address() {
		return false
	}
	sameAttribute := func(attribute int32) bool {
		oldSlot, newSlot := slices.Index(oldParent.Attributes, attribute), slices.Index(parent.Attributes, attribute)
		return oldSlot >= 0 && newSlot >= 0 && oldParent.Columns[oldSlot].Usage == parent.Columns[newSlot].Usage
	}
	for _, key := range index.SQLIndex.Keys {
		if key.Derived == nil {
			if !sameAttribute(key.Attribute) {
				return false
			}
			continue
		}
		for _, dependency := range key.Derived.Dependencies {
			a := dependency.Address
			if a.ClassID == parent.Address().ClassID && a.ObjectID == parent.Address().ObjectID && a.SubID != 0 && !sameAttribute(a.SubID) {
				return false
			}
		}
	}
	for _, attribute := range index.SQLIndex.Include {
		if !sameAttribute(attribute) {
			return false
		}
	}
	if predicate := index.SQLIndex.Predicate; predicate != nil {
		for _, dependency := range predicate.Dependencies {
			a := dependency.Address
			if a.ClassID == parent.Address().ClassID && a.ObjectID == parent.Address().ObjectID && a.SubID != 0 && !sameAttribute(a.SubID) {
				return false
			}
		}
	}
	return true
}

// Only referenced dense slots affect index extraction authority. Dropping an
// unrelated tail or attaching metadata does not replace an immutable root.
func indexLayoutSlotsUnchanged(index, oldParent, parent *catalog.Relation) bool {
	for oldSlot, attribute := range oldParent.Attributes {
		if indexAttributeAffected(index.SQLIndex, attribute) && slices.Index(parent.Attributes, attribute) != oldSlot {
			return false
		}
	}
	return true
}

// LayoutCapture is one layout lineage of a parent and the index root it
// transports, captured under Engine.mu.
type LayoutCapture struct {
	Lineage LayoutLineage
	Root    *storage.IndexRoot
}

// TransportCapturedIndexLayouts moves the captured roots and private row
// outputs to the target layout. It runs without Engine.mu.
func TransportCapturedIndexLayouts(evaluation *exec.Context, changes *session.Journal, captures []LayoutCapture, sourceStore, targetStore *storage.Relation, sourceWrites, writes []storage.RowVersion) ([]storage.RowVersion, bool, error) {
	if len(captures) == 0 {
		return writes, false, nil
	}
	if sourceStore == nil || targetStore == nil || len(sourceStore.Versions) != len(targetStore.Versions) {
		return nil, false, ids.ErrCatalogReplaced
	}
	for i, row := range sourceStore.Versions {
		if err := evaluation.Context.Err(); err != nil {
			return nil, false, err
		}
		next := targetStore.Versions[i]
		if row.Row != next.Row || row.Version != next.Version {
			return nil, false, ids.ErrCatalogReplaced
		}
	}
	for _, captured := range captures {
		lineage, root := captured.Lineage, captured.Root
		if sourceStore == targetStore && SameIndexKeySemantics(lineage.Source, lineage.Target) {
			continue
		}
		if root.Hash != nil && SameIndexKeySemantics(lineage.Source, lineage.Target) && root.Hash.Storage == targetStore.IndexIdentity() && root.Hash.Generation == targetStore.Generation && root.Hash.Ends == targetStore.Ends && root.Hash.Prefix == len(targetStore.Versions) {
			RetainIndexBuild(changes, lineage.Target, targetStore, root)
			continue
		}
		if root.Hash != nil {
			for _, bucket := range root.Hash.Buckets {
				for _, entry := range bucket {
					if err := evaluation.Context.Err(); err != nil {
						return nil, false, err
					}
					if entry.Position < 0 || entry.Position >= len(sourceStore.Versions) {
						return nil, false, ids.ErrCatalogReplaced
					}
					row := sourceStore.Versions[entry.Position]
					if row.Row != entry.Row || row.Version != entry.Version {
						return nil, false, ids.ErrCatalogReplaced
					}
				}
			}
			binding, err := BindHashIndex(lineage.TargetParent, lineage.Target.SQLIndex)
			if err != nil {
				return nil, false, err
			}
			next := *root.Hash
			next.Binding, next.Storage, next.Generation = binding, targetStore.IndexIdentity(), targetStore.Generation
			next.Prefix, next.Ends = len(targetStore.Versions), targetStore.Ends
			if err := next.CaptureRowEntries(evaluation.Context, lineage.Target.SQLIndex); err != nil {
				return nil, false, err
			}
			root = &storage.IndexRoot{Hash: &next}
		} else {
			for _, entry := range root.Entries {
				if err := evaluation.Context.Err(); err != nil {
					return nil, false, err
				}
				if entry.Position < 0 || entry.Position >= len(sourceStore.Versions) {
					return nil, false, ids.ErrCatalogReplaced
				}
				row := sourceStore.Versions[entry.Position]
				if row.Row != entry.Row || row.Version != entry.Version {
					return nil, false, ids.ErrCatalogReplaced
				}
			}
			if !indexLayoutSlotsUnchanged(lineage.Source, lineage.SourceParent, lineage.TargetParent) {
				root = NewLogicalIndexRoot(lineage.Target.SQLIndex, root.Entries)
			}
		}
		RetainIndexBuild(changes, lineage.Target, targetStore, root)
	}
	if len(writes) == 0 {
		return writes, false, nil
	}
	pending := false
	for _, captured := range captures {
		pending = pending || captured.Lineage.Target.SQLIndex.HasRuntimeDomainConversion()
	}
	for _, row := range sourceWrites {
		pending = pending || len(row.IndexOutputs) != 0
	}
	if !pending {
		return writes, false, nil
	}
	positions := make(map[RowIdentity]int, len(sourceWrites))
	for i, row := range sourceWrites {
		positions[RowIdentity{Row: row.Row, Version: row.Version}] = i
	}
	copied := false
	for i, row := range writes {
		if err := evaluation.Context.Err(); err != nil {
			return nil, false, err
		}
		position, ok := positions[RowIdentity{Row: row.Row, Version: row.Version}]
		if !ok {
			return nil, false, ids.ErrCatalogReplaced
		}
		var outputs map[ids.ObjectAddress]storage.IndexOutput
		for _, captured := range captures {
			lineage := captured.Lineage
			output, ok := sourceWrites[position].IndexOutputs[lineage.Source.Address()]
			if !ok && !lineage.Target.SQLIndex.HasRuntimeDomainConversion() {
				continue
			}
			if !ok || (!OutputMatches(output, lineage.Source) && !OutputMatches(output, lineage.Target)) {
				return nil, false, fmt.Errorf("stuntgres: layout projection is missing produced private index data")
			}
			if outputs == nil {
				outputs = maps.Clone(row.IndexOutputs)
				if outputs == nil {
					outputs = make(map[ids.ObjectAddress]storage.IndexOutput)
				}
			}
			output.Index = lineage.Target
			outputs[lineage.Target.Address()] = output
		}
		if outputs != nil {
			if !copied {
				writes = slices.Clone(writes)
				copied = true
			}
			writes[i].IndexOutputs = outputs
		}
	}
	return writes, copied, nil
}

// A real whole-storage deletion has no SQL index inputs. Rebind its empty
// physical artifacts to the new incarnation rather than retaining old pins or
// evaluating declarations during publication. record, when set, receives each
// retained candidate for the catalog stage.
func TransportEmptyIndexStorage(ctx context.Context, snapshot *catalog.State, changes *session.Journal, record func(ids.ObjectAddress, RootCandidate), address ids.ObjectAddress, store *storage.Relation) error {
	parent, ok := snapshot.Relation(address)
	if !ok || store == nil || len(store.Versions) != 0 {
		return ids.ErrCatalogReplaced
	}
	for _, descriptor := range snapshot.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != address {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !index.SQLIndex.Ready || !index.SQLIndex.Live || parent.RelationKind == catalog.PartitionedRelation {
			JournalOf(changes.Indexes).Forget(index.Address())
			continue
		}
		var root *storage.IndexRoot
		if index.SQLIndex.Method == catalog.HashMethod {
			binding, err := BindHashIndex(parent, index.SQLIndex)
			if err != nil {
				return err
			}
			root = &storage.IndexRoot{Hash: &storage.HashRoot{Buckets: make(map[uint64][]storage.HashEntry), Binding: binding, Storage: store.IndexIdentity(), Generation: store.Generation, Ends: store.Ends}}
		} else {
			if err := bindIndexProvider(parent, index.SQLIndex); err != nil {
				return err
			}
			root = NewLogicalIndexRoot(index.SQLIndex, nil)
		}
		RetainIndexBuild(changes, index, store, root)
		if record != nil {
			record(index.Address(), JournalIndexesOrZero(changes).Candidates[index.Address()])
		}
	}
	return ctx.Err()
}
