package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type truncateName struct {
	name      parser.ObjectName
	ref       ids.ObjectRef
	namespace ids.ObjectAddress
}
type truncateGraphFence struct {
	base      *catalog.State
	targets   map[ids.ObjectAddress]int // Immutable after acquisition; savepoints share it.
	token     ids.SessionToken          // Descendant TEMP exclusion matches grant-time admission.
	restart   bool
	sequences map[ids.ObjectAddress]*storage.SequenceGenerator // Immutable committed bindings; not counter undo.
}

func (truncateGraphFence) Kind() session.FenceKind { return fenceTruncateGraph }

// Catalog replacements do not have to honor SQL AX locks. Fence new inbound
// FK/hierarchy edges as well as the exact selected descriptors and stores.
func (fence truncateGraphFence) currentLocked(e *Engine, c *catalogChanges) error {
	current := e.privateCatalogLocked(c)
	if fence.restart {
		for address, generator := range fence.sequences {
			if e.storage.Sequences[address] != generator {
				return catalog.Conflict{}
			}
		}
		for address, descriptor := range current.Descriptors() {
			d, ok := descriptor.(*catalog.Relation)
			if !ok || !truncateOwnsSequence(d, fence.targets) {
				continue
			}
			if _, observed := fence.sequences[address]; observed {
				continue
			}
			// Legitimate own subsequent CREATE/OWNED BY is not an incoming
			// host edge. Its first committed predecessor is fenced by the
			// ordinary originals check, including changes hidden by overlays.
			if _, own := c.Descriptors[address]; !own {
				return catalog.Conflict{}
			}
		}
	}
	for address, descriptor := range current.Descriptors() {
		if descriptor == fence.base.Get(address) || fence.targets[address] != 0 {
			continue
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, fk := range d.Constraints.ForeignKeys {
			if fence.targets[fk.Target] != 0 {
				return catalog.Conflict{}
			}
		}
		if d.Inheritance != nil || d.Membership != nil {
			otherTemporary, err := truncateOtherTemporary(current, d, fence.token)
			if err != nil {
				return catalog.Conflict{}
			}
			if otherTemporary {
				continue
			}
		}
		if d.Inheritance != nil {
			for _, parent := range d.Inheritance.Parents {
				if fence.targets[parent] != 0 {
					return catalog.Conflict{}
				}
			}
		}
		if d.Membership != nil && fence.targets[d.Membership.Parent] != 0 {
			return catalog.Conflict{}
		}
	}
	return nil
}

// stageTruncate replaces roots, never scans/discards individual row values.
// Existing Finish/Commit provides the one atomic publication frontier and veto.
func (s *StatementScope) stageTruncate() error {
	groups, err := s.prepareForeignTruncate()
	if err != nil {
		return err
	}
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	for _, named := range s.truncateOrZero().names {
		if lookupErr := e.truncateNameCurrent(auth, named); lookupErr != nil {
			e.mu.Unlock()
			return lookupErr
		}
	}
	changes := s.owner.catalog.Clone()
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, s.op.Session)
	for _, selected := range s.truncateOrZero().relations {
		ref := selected.ref
		d, ok := auth.Catalog.Relation(ref.Address)
		store := e.privateStorageLocked(s.owner.catalog, ref.Address)
		if !ok || d.Namespace() != selected.relation.Namespace() || !sameRelationRowShape(d, selected.relation) || store != selected.storage {
			err = ids.ErrCatalogReplaced
			break
		}
		if err = checkTruncate(auth, ref, d, selected.privilege, false); err != nil {
			break
		}
		if err = s.relationCursorPreflight(auth.Catalog, ref.Address, "TRUNCATE"); err != nil {
			break
		}
		stage.querySources = append(stage.querySources, d)
		if d.RelationKind == catalog.PartitionedRelation || d.RelationKind == catalog.ForeignRelation {
			continue
		}
		if store == nil {
			err = fmt.Errorf("stuntgres: relation storage is missing")
			break
		}
		if store.Generation == ^uint64(0) {
			err = fmt.Errorf("stuntgres: storage generation exhausted")
			break
		}
		shape, exists := changes.Storage[ref.Address]
		if !exists {
			original := e.storage.Relations[ref.Address]
			if original == nil {
				err = ids.ErrCatalogReplaced
				break
			}
			shape = catalogStorageChange{Original: original, Root: original.Versions, Ends: original.Ends, Generation: original.Generation}
		}
		// Do not bridge a preceding ordinary private storage rewrite. The marker
		// means the entire transition from the first committed predecessor is pure.
		shape.Truncated = !exists || shape.Truncated || shape.Replacement == shape.Original
		lineage := store.TruncateLineage
		if lineage == nil {
			lineage = &storage.TruncateLineage{}
		}
		shape.Replacement = &storage.Relation{Generation: store.Generation + 1, TruncateLineage: lineage}
		shape.Relation = d
		shape.Rewritten, shape.Frontier = true, e.visibility.CommitSeq()
		shape.Snapshot, shape.HasSnapshot = nil, false
		shape.Fills = nil
		changes.Storage[ref.Address] = shape
		e.fenceEmptyIndexStorageLocked(auth.Catalog.State(), changes, ref.Address)
		if err = e.serializableWriteLocked(s, ref, []visibility.SerializableWrite{{Rewrite: true}}); err != nil {
			break
		}
	}
	if err == nil {
		// Recheck closure against the same coherent catalog used to capture stores.
		// SQL installation is blocked by selected AX; host replacement is fenced.
		if len(truncateReferencers(auth.Catalog, s.truncateOrZero().selected)) != 0 {
			err = catalog.Conflict{}
		}
	}
	var sequences map[ids.ObjectAddress]*storage.SequenceGenerator
	if err == nil && s.truncateOrZero().restart {
		sequences, err = s.stageTruncateSequences(auth, stage)
	}
	if err == nil {
		changes.AddFence(truncateGraphFence{base: auth.Catalog.State(), targets: s.truncateOrZero().selected, token: s.op.Session.SessionToken, restart: s.truncateOrZero().restart, sequences: sequences})
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return err
	}
	for _, selected := range s.truncateOrZero().relations {
		address := selected.ref.Address
		if selected.relation.RelationKind != catalog.PartitionedRelation && selected.relation.RelationKind != catalog.ForeignRelation {
			if err := transportEmptyIndexStorage(s.op.Context(), auth.Catalog.State(), changes, stage, address, changes.Storage[address].Replacement); err != nil {
				return err
			}
		}
		if len(s.ownWrites[address]) != 0 || s.ownDeletes[address] != nil {
			s.stageProjectedColumnRoots(address, nil, nil)
		}
	}
	if err := e.stageCatalogOverlay(s, stage, changes); err != nil {
		return err
	}
	return s.executeForeignTruncate(groups)
}

// A name observation is a fence, not permission to replay on a new relation.
func (e *Engine) truncateNameCurrent(auth AuthorizationView, named truncateName) error {
	ref, err := e.resolveRelationReference(auth, named.name, true)
	if diagnostic, ok := err.(*sqlstate.Error); ok && (diagnostic.Code == "42P01" || diagnostic.Code == "3F000") {
		return ids.ErrCatalogReplaced
	}
	if err != nil {
		return err
	}
	if ref != named.ref {
		return ids.ErrCatalogReplaced
	}
	descriptor, ok := auth.Catalog.Relation(ref.Address)
	if !ok || descriptor.Namespace() != named.namespace {
		return ids.ErrCatalogReplaced
	}
	return nil
}
