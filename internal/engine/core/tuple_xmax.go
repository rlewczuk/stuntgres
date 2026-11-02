package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
)

// CaptureTupleXmax owns a demand-only immutable presentation vector. Raw XIDs
// (including MultiXact IDs and aborted owners) never enter RowVersion's full-XID
// fields or any mutation candidate. All manager work is outside metadata locks.
func (h *statementHost) CaptureTupleXmax(ref ids.ObjectRef, rows []storage.RowVersion) ([]uint32, error) {
	s := h.scope()
	e := s.Engine()
	if len(rows) == 0 {
		return nil, nil
	}
	facts := make([]lock.TupleMaintenance, len(rows))
	result := make([]uint32, len(rows))
	for {
		if err := s.Active(); err != nil {
			return nil, err
		}
		e.Mu.Lock()
		store := e.PrivateStorageLocked(s.PendingCatalog(), ref.Address)
		var headers *storage.HeapHeaderRoot
		if store != nil {
			headers = store.Headers
		}
		var err error
		for i, row := range rows {
			facts[i] = lock.TupleMaintenance{}
			facts[i].Tuple.Resource, err = s.tupleResourceLocked(ref, row, store)
			if err != nil {
				break
			}
			facts[i].Tuple.Version = row.Version
			result[i] = store.Headers.Apply(row).HeapXmax()
		}
		e.Mu.Unlock()
		if err != nil {
			return nil, err
		}
		if err = e.Locks.CaptureTupleMaintenance(s.Op.Context(), s.LockTicket, facts); err != nil {
			return nil, err
		}
		e.Mu.Lock()
		changed := store != nil && store.Headers != headers
		for i, row := range rows {
			var resource lock.Resource
			resource, err = s.tupleResourceLocked(ref, row, store)
			if err != nil {
				break
			}
			if resource != facts[i].Tuple.Resource {
				changed = true
				break
			}
		}
		e.Mu.Unlock()
		if err != nil {
			return nil, err
		}
		if changed {
			continue
		}
		for i, fact := range facts {
			if fact.Present {
				result[i] = fact.Xmax
			}
		}
		return result, nil
	}
}
