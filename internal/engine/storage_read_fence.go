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

// newStorageReadFence records store, the storage read at a; a nil store
// records its absence.
func newStorageReadFence(a ids.ObjectAddress, store *storage.Relation) storageReadFence {
	f := storageReadFence{address: a, storage: store}
	if store != nil {
		f.length, f.ends, f.generation = len(store.Versions), store.Ends, store.Generation
	}
	return f
}

func (storageReadFence) Kind() session.FenceKind { return fenceStorageRead }

// matches reports whether s is the recorded storage, unchanged.
func (f storageReadFence) matches(s *storage.Relation) bool {
	return s == f.storage && (s == nil || len(s.Versions) == f.length && s.Ends == f.ends && s.Generation == f.generation)
}

func (f storageReadFence) current(_ *catalog.State, relations map[ids.ObjectAddress]*storage.Relation) error {
	if !f.matches(relations[f.address]) {
		return catalog.Conflict{}
	}
	return nil
}

func (e *Engine) storageReadsValidLocked(stage *CatalogStage) bool {
	for _, read := range stage.storageReads {
		if current := e.stageStorageLocked(stage, read.address); current == nil || !read.matches(current) {
			return false
		}
	}
	return true
}
