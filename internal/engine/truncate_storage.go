package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Nonzero-size tokens identify only uninterrupted TRUNCATE incarnations.
// Engine.mu protects predecessor stamping and all token comparisons. Tokens
// retain neither old storage nor row vectors; ordinary replacements break them.
type truncateLineage struct{ marker byte }

// SameTruncateLineage reports whether a and b are incarnations of one
// uninterrupted TRUNCATE lineage. Either may be nil.
func (a *relationStorage) SameTruncateLineage(b *relationStorage) bool {
	return a != nil && b != nil && a.TruncateLineage != nil && a.TruncateLineage == b.TruncateLineage
}

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
	sequences map[ids.ObjectAddress]*sequenceGenerator // Immutable committed bindings; not counter undo.
}

// Catalog replacements do not have to honor SQL AX locks. Fence new inbound
// FK/hierarchy edges as well as the exact selected descriptors and stores.
func (e *Engine) truncateGraphPreflightLocked(c *catalogChanges) error {
	if len(c.truncateFences) == 0 {
		return nil
	}
	current := e.privateCatalogLocked(c)
	for _, fence := range c.truncateFences {
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
				if _, own := c.descriptors[address]; !own {
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
	}
	return nil
}

// stageTruncate replaces roots, never scans/discards individual row values.
// Existing Finish/Commit provides the one atomic publication frontier and veto.
func (s *StatementScope) stageTruncate() error {
	e := s.op.session.Engine()
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
	for _, named := range s.truncateNames {
		if lookupErr := e.truncateNameCurrent(auth, named); lookupErr != nil {
			e.mu.Unlock()
			return lookupErr
		}
	}
	changes := cloneCatalogChanges(s.owner.catalog)
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, s.op.session)
	for _, selected := range s.truncateRelations {
		ref := selected.ref
		d, ok := auth.catalog.Relation(ref.Address)
		store := e.privateStorageLocked(s.owner.catalog, ref.Address)
		if !ok || d.Namespace() != selected.relation.Namespace() || !sameRelationRowShape(d, selected.relation) || store != selected.storage {
			err = ids.ErrCatalogReplaced
			break
		}
		if err = checkTruncate(auth, ref, d, selected.privilege, false); err != nil {
			break
		}
		if err = s.relationCursorPreflight(auth.catalog, ref.Address, "TRUNCATE"); err != nil {
			break
		}
		stage.querySources = append(stage.querySources, d)
		if d.RelationKind == catalog.PartitionedRelation {
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
		shape, exists := changes.storage[ref.Address]
		if !exists {
			original := e.storage.Relations[ref.Address]
			if original == nil {
				err = ids.ErrCatalogReplaced
				break
			}
			shape = catalogStorageChange{original: original, root: original.Versions, ends: original.Ends, generation: original.Generation}
		}
		// Do not bridge a preceding ordinary private storage rewrite. The marker
		// means the entire transition from the first committed predecessor is pure.
		shape.truncated = !exists || shape.truncated || shape.replacement == shape.original
		lineage := store.TruncateLineage
		if lineage == nil {
			lineage = &truncateLineage{}
		}
		shape.replacement = &relationStorage{Generation: store.Generation + 1, TruncateLineage: lineage}
		shape.relation = d
		shape.rewritten, shape.frontier = true, e.commitSeq
		shape.snapshot, shape.hasSnapshot = nil, false
		shape.fills = nil
		changes.storage[ref.Address] = shape
		e.fenceEmptyIndexStorageLocked(auth.catalog.State(), changes, ref.Address)
		if err = e.serializableWriteLocked(s, ref, []serializableWrite{{rewrite: true}}); err != nil {
			break
		}
	}
	if err == nil {
		// Recheck closure against the same coherent catalog used to capture stores.
		// SQL installation is blocked by selected AX; host replacement is fenced.
		if len(truncateReferencers(auth.catalog, s.truncateSelected)) != 0 {
			err = catalog.Conflict{}
		}
	}
	var sequences map[ids.ObjectAddress]*sequenceGenerator
	if err == nil && s.truncateRestart {
		sequences, err = s.stageTruncateSequences(auth, stage)
	}
	if err == nil {
		changes.truncateFences = append(changes.truncateFences, truncateGraphFence{base: auth.catalog.State(), targets: s.truncateSelected, token: s.op.session.token, restart: s.truncateRestart, sequences: sequences})
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return err
	}
	for _, selected := range s.truncateRelations {
		address := selected.ref.Address
		if selected.relation.RelationKind != catalog.PartitionedRelation {
			if err := transportEmptyIndexStorage(s.op.Context(), auth.catalog.State(), changes, stage, address, changes.storage[address].replacement); err != nil {
				return err
			}
		}
		if len(s.ownWrites[address]) != 0 || s.ownDeletes[address] != nil {
			s.stageProjectedColumnRoots(address, nil, nil)
		}
	}
	return e.stageCatalogOverlay(s, stage, changes)
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
	descriptor, ok := auth.catalog.Relation(ref.Address)
	if !ok || descriptor.Namespace() != named.namespace {
		return ids.ErrCatalogReplaced
	}
	return nil
}
