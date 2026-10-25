package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

// truncateGraphFence is TRUNCATE's catalog fence over the graph it admitted.
type truncateGraphFence struct{ dml.TruncateGraph }

func (truncateGraphFence) Kind() session.FenceKind { return fenceTruncateGraph }

// CurrentLocked checks the admitted graph against the private catalog and the
// published sequence generators. Caller holds Engine.mu.
func (fence truncateGraphFence) CurrentLocked(e *Engine, c *session.Journal) error {
	return fence.CheckCurrent(e.PrivateCatalogLocked(c), c, e.Storage.Sequences)
}

// stageTruncate replaces roots, never scans/discards individual row values.
// Existing Finish/Commit provides the one atomic publication frontier and veto.
func (s *StatementScope) stageTruncate() error {
	groups, err := s.prepareForeignTruncate()
	if err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	for _, named := range s.truncateOrZero().Names {
		if lookupErr := e.truncateNameCurrent(auth, named); lookupErr != nil {
			e.Mu.Unlock()
			return lookupErr
		}
	}
	changes := s.Owner.Catalog.Clone()
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, s.Op.Session)
	for _, selected := range s.truncateOrZero().Relations {
		ref := selected.Ref
		d, ok := auth.Catalog.Relation(ref.Address)
		store := e.PrivateStorageLocked(s.Owner.Catalog, ref.Address)
		if !ok || d.Namespace() != selected.Relation.Namespace() || !index.SameRelationRowShape(d, selected.Relation) || store != selected.Storage {
			err = ids.ErrCatalogReplaced
			break
		}
		if err = dml.CheckTruncate(auth.View, auth, ref, d, selected.Privilege, false); err != nil {
			break
		}
		if err = s.relationCursorPreflight(auth.Catalog, ref.Address, "TRUNCATE"); err != nil {
			break
		}
		stage.QuerySources = append(stage.QuerySources, d)
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
			original := e.Storage.Relations[ref.Address]
			if original == nil {
				err = ids.ErrCatalogReplaced
				break
			}
			shape = session.StorageChange{Original: original, Root: original.Versions, Ends: original.Ends, Generation: original.Generation}
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
		shape.Rewritten, shape.Frontier = true, e.Visibility.CommitSeq()
		shape.Snapshot, shape.HasSnapshot = nil, false
		shape.Fills = nil
		changes.Storage[ref.Address] = shape
		e.fenceEmptyIndexStorageLocked(auth.Catalog.State(), changes, ref.Address)
		if err = e.SerializableWriteLocked(s, ref, []visibility.SerializableWrite{{Rewrite: true}}); err != nil {
			break
		}
	}
	if err == nil {
		// Recheck closure against the same coherent catalog used to capture stores.
		// SQL installation is blocked by selected AX; host replacement is fenced.
		if len(dml.TruncateReferencers(auth.Catalog, s.truncateOrZero().Selected)) != 0 {
			err = catalog.Conflict{}
		}
	}
	var sequences map[ids.ObjectAddress]*storage.SequenceGenerator
	if err == nil && s.truncateOrZero().Restart {
		sequences, err = s.stageTruncateSequences(auth, stage)
	}
	if err == nil {
		changes.AddFence(truncateGraphFence{dml.TruncateGraph{Base: auth.Catalog.State(), Targets: s.truncateOrZero().Selected, Token: s.Op.Session.SessionToken, Restart: s.truncateOrZero().Restart, Sequences: sequences}})
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return err
	}
	for _, selected := range s.truncateOrZero().Relations {
		address := selected.Ref.Address
		if selected.Relation.RelationKind != catalog.PartitionedRelation && selected.Relation.RelationKind != catalog.ForeignRelation {
			if err := index.TransportEmptyIndexStorage(s.Op.Context(), auth.Catalog, changes, stage.recordIndexCandidate, address, changes.Storage[address].Replacement); err != nil {
				return err
			}
		}
		if len(s.OwnWrites[address]) != 0 || s.OwnDeletes[address] != nil {
			s.StageProjectedColumnRoots(address, nil, nil)
		}
	}
	if err := e.StageCatalogOverlay(s, stage, changes); err != nil {
		return err
	}
	return s.executeForeignTruncate(groups)
}

// A name observation is a fence, not permission to replay on a new relation.
func (e *Engine) truncateNameCurrent(auth AuthorizationView, named dml.TruncateName) error {
	ref, err := e.ResolveRelationReference(auth, named.Name, true)
	if diagnostic, ok := err.(*sqlstate.Error); ok && (diagnostic.Code == "42P01" || diagnostic.Code == "3F000") {
		return ids.ErrCatalogReplaced
	}
	if err != nil {
		return err
	}
	if ref != named.Ref {
		return ids.ErrCatalogReplaced
	}
	descriptor, ok := auth.Catalog.Relation(ref.Address)
	if !ok || descriptor.Namespace() != named.Namespace {
		return ids.ErrCatalogReplaced
	}
	return nil
}
