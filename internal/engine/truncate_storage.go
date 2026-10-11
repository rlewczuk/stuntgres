package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Nonzero-size tokens identify only uninterrupted TRUNCATE incarnations.
// Engine.mu protects predecessor stamping and all token comparisons. Tokens
// retain neither old storage nor row vectors; ordinary replacements break them.
type truncateLineage struct{ marker byte }

func sameTruncateLineage(a, b *relationStorage) bool {
	return a != nil && b != nil && a.truncateLineage != nil && a.truncateLineage == b.truncateLineage
}

type truncateName struct {
	name      parser.ObjectName
	ref       ObjectRef
	namespace ObjectAddress
}
type truncateGraphFence struct {
	base    *catalogState
	targets map[ObjectAddress]int // Immutable after acquisition; savepoints share it.
	token   SessionToken          // Descendant TEMP exclusion matches grant-time admission.
}

// Catalog replacements do not have to honor SQL AX locks. Fence new inbound
// FK/hierarchy edges as well as the exact selected descriptors and stores.
func (e *Engine) truncateGraphPreflightLocked(c *catalogChanges) error {
	if len(c.truncateFences) == 0 {
		return nil
	}
	current := e.privateCatalogLocked(c)
	for _, fence := range c.truncateFences {
		for address, descriptor := range current.state.descriptors {
			if descriptor == fence.base.descriptors[address] || fence.targets[address] != 0 {
				continue
			}
			d, ok := descriptor.(*relationDescriptor)
			if !ok {
				continue
			}
			for _, fk := range d.foreignKeys {
				if fence.targets[fk.Target] != 0 {
					return CatalogConflict{}
				}
			}
			if d.inheritance != nil || d.membership != nil {
				otherTemporary, err := truncateOtherTemporary(current, d, fence.token)
				if err != nil {
					return CatalogConflict{}
				}
				if otherTemporary {
					continue
				}
			}
			if d.inheritance != nil {
				for _, parent := range d.inheritance.parents {
					if fence.targets[parent] != 0 {
						return CatalogConflict{}
					}
				}
			}
			if d.membership != nil && fence.targets[d.membership.parent] != 0 {
				return CatalogConflict{}
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
	if s.txRef.epoch != e.state.epoch {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	for _, named := range s.truncateNames {
		if lookupErr := e.truncateNameCurrent(auth, named); lookupErr != nil {
			e.mu.Unlock()
			return lookupErr
		}
	}
	changes := cloneCatalogChanges(s.owner.catalog)
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = s.op.session, auth.revision, auth.roles.state.revision
	for _, selected := range s.truncateRelations {
		ref := selected.ref
		d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		store := e.privateStorageLocked(s.owner.catalog, ref.Address)
		if !ok || d.namespace != selected.relation.namespace || !sameRelationRowShape(d, selected.relation) || store != selected.storage {
			err = ErrCatalogReplaced
			break
		}
		if err = checkTruncate(auth, ref, d, selected.privilege, false); err != nil {
			break
		}
		if err = s.relationCursorPreflight(auth.catalog, ref.Address, "TRUNCATE"); err != nil {
			break
		}
		stage.querySources = append(stage.querySources, d)
		if d.relationKind == partitionedRelation {
			continue
		}
		if store == nil {
			err = fmt.Errorf("stuntgres: relation storage is missing")
			break
		}
		if store.generation == ^uint64(0) {
			err = fmt.Errorf("stuntgres: storage generation exhausted")
			break
		}
		shape, exists := changes.storage[ref.Address]
		if !exists {
			original := e.storage[ref.Address]
			if original == nil {
				err = ErrCatalogReplaced
				break
			}
			shape = catalogStorageChange{original: original, root: original.versions, ends: original.ends, generation: original.generation}
		}
		// Do not bridge a preceding ordinary private storage rewrite. The marker
		// means the entire transition from the first committed predecessor is pure.
		shape.truncated = !exists || shape.truncated || shape.replacement == shape.original
		lineage := store.truncateLineage
		if lineage == nil {
			lineage = &truncateLineage{marker: 1}
		}
		shape.replacement = &relationStorage{generation: store.generation + 1, truncateLineage: lineage}
		shape.relation = d
		shape.rewritten, shape.frontier = true, e.commitSeq
		shape.snapshot, shape.hasSnapshot = nil, false
		shape.fills = nil
		changes.storage[ref.Address] = shape
		if err = e.serializableWriteLocked(s, ref, []serializableWrite{{rewrite: true}}); err != nil {
			break
		}
	}
	if err == nil {
		// Recheck closure against the same coherent catalog used to capture stores.
		// SQL installation is blocked by selected AX; host replacement is fenced.
		if len(truncateReferencers(auth.catalog, s.truncateSelected)) != 0 {
			err = CatalogConflict{}
		}
	}
	if err == nil {
		changes.truncateFences = append(changes.truncateFences, truncateGraphFence{base: auth.catalog.state, targets: s.truncateSelected, token: s.op.session.token})
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return err
	}
	for _, selected := range s.truncateRelations {
		address := selected.ref.Address
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
		return ErrCatalogReplaced
	}
	if err != nil {
		return err
	}
	if ref != named.ref {
		return ErrCatalogReplaced
	}
	descriptor, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || descriptor.namespace != named.namespace {
		return ErrCatalogReplaced
	}
	return nil
}
