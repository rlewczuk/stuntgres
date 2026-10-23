package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/partition"
)

func (s *StatementScope) capturePartitionInsertLocked(e *Engine, auth AuthorizationView, target *catalog.Relation) error {
	if !catalog.HierarchyRelation(target) {
		return nil
	}
	state, err := partition.CaptureInsertLocked(auth.Catalog, auth.View, auth, target, func() error {
		if s.output.RegTS != nil {
			return nil
		}
		return e.captureCommentSnapshotLocked(s)
	}, func(leaf *catalog.Relation) error {
		if err := e.captureCheckCommentSnapshotLocked(s, leaf); err != nil {
			return err
		}
		return e.captureCheckVisibilitySnapshotLocked(s, leaf)
	})
	if err != nil {
		return err
	}
	s.PartitionInsert = state
	return nil
}
