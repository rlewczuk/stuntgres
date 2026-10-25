package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/partition"
)

func (s *StatementScope) capturePartitionInsertLocked(e *Engine, auth AuthorizationView, target *catalog.Relation) error {
	if !catalog.HierarchyRelation(target) {
		return nil
	}
	read := partition.CaptureCatalog(auth.Catalog, s.PartitionRelations)
	target = read.MustRelation(target.Address())
	state, err := partition.CaptureInsertLocked(read, auth.View, auth, target, func() error {
		if s.Output.RegTS != nil {
			return nil
		}
		return e.CaptureCommentSnapshotLocked(s)
	}, func(leaf *catalog.Relation) error {
		if err := e.CaptureCheckCommentSnapshotLocked(s, leaf); err != nil {
			return err
		}
		return e.CaptureCheckVisibilitySnapshotLocked(s, leaf)
	})
	if err != nil {
		return err
	}
	s.PartitionInsert = state
	return nil
}
