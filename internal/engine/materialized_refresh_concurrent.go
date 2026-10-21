package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
)

type materializedSourceFence struct {
	ref      ids.ObjectRef
	relation *catalog.Relation
	storage  *storage.Relation
}

func (materializedSourceFence) Kind() session.FenceKind { return fenceMaterializedSource }

func (f materializedSourceFence) currentLocked(e *Engine, _ *session.Journal) error {
	if !e.materializedSourceFenceCurrent(f) {
		return catalog.Conflict{}
	}
	return nil
}

func (e *Engine) materializedSourceFenceCurrent(f materializedSourceFence) bool {
	if f.ref.Domain() != e.catalog.Domain || f.ref.Epoch != e.catalog.state.Epoch() || f.relation == nil || f.storage == nil || f.ref.Address != f.relation.Address() || f.relation.RelationKind != catalog.MaterializedViewRelation {
		return false
	}
	current, _ := e.catalog.state.Relation(f.ref.Address)
	store := e.storage.Relations[f.ref.Address]
	return current == f.relation && store == f.storage || view.SameMaterializedLineage(f.relation, current, f.storage, store)
}
