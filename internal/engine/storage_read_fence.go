package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// storageReadFence records the committed storage a stage read, so publication
// fails when that storage changed. Partition CREATE and ATTACH scans, journal
// reads and overlay-index reads record it.
type storageReadFence struct {
	address    ids.ObjectAddress
	storage    *storage.Relation
	length     int
	ends       *storage.DeletionRoot
	generation uint64
}

func (storageReadFence) Kind() session.FenceKind { return fenceStorageRead }

func (f storageReadFence) currentLocked(e *Engine, _ *session.Journal) error {
	s := e.storage.Relations[f.address]
	if s != f.storage || s != nil && (len(s.Versions) != f.length || s.Ends != f.ends || s.Generation != f.generation) {
		return catalog.Conflict{}
	}
	return nil
}

func (e *Engine) storageReadsValidLocked(stage *CatalogStage) bool {
	for _, read := range stage.storageReads {
		current := e.stageStorageLocked(stage, read.address)
		if current != read.storage || current == nil || len(current.Versions) != read.length || current.Ends != read.ends || current.Generation != read.generation {
			return false
		}
	}
	return true
}
