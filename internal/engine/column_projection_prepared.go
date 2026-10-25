package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Project a DROP shape through committed, private-write and retained snapshot
// roots. The original publication preconditions remain in shape.
func projectColumnShape(scope *core.StatementScope, old, next *catalog.Relation, store *storage.Relation, shape session.StorageChange, snapshot visibility.Root, snapshotExists bool) (session.StorageChange, []storage.RowVersion, *visibility.PrivateDeletes, error) {
	writes, deletes := scope.ColumnProjectionRoots(old.Address())
	return session.ProjectColumnShapeRoots(scope.Op.Context(), scope.Owner.Isolation.Stable(), old, next, store, shape, snapshot, snapshotExists, writes, deletes)
}

func stagePreparedColumnShape(scope *core.StatementScope, stage *core.CatalogStage, changes *session.Journal, old, next *catalog.Relation) error {
	return scope.StageColumnShape(stage, changes, old, next, true)
}
