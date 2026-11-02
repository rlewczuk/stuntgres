package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// All helpers run under owner admission/Mu (or a prepared claim), then Engine.Mu.
// A batch owns actual immutable headers and shared payloads, never an address-
// derived tuple or a new installed relation. Keeping it private avoids changing
// the generation fenced by a surviving catalog journal.
func (e *Engine) captureDiscardedRowsLocked(ref ids.TxRef, journal *session.Journal, address ids.ObjectAddress, rows, keep []storage.RowVersion) session.HeapDiscard {
	if len(rows) == 0 || storage.SameVersionRoot(rows, keep) || ref.Domain() != e.Catalog.Domain || ref.Epoch() != e.Catalog.State.Epoch() {
		return session.HeapDiscard{}
	}
	store := e.PrivateStorageLocked(journal, address)
	if store == nil {
		return session.HeapDiscard{}
	}
	versions, _ := storage.RetainDiscardedVersions(nil, rows, keep)
	return session.HeapDiscard{Address: address, Storage: store, Versions: versions}
}

func appendHeapDiscard(batches []session.HeapDiscard, batch session.HeapDiscard) []session.HeapDiscard {
	if batch.Versions == nil {
		return batches
	}
	for _, prior := range batches {
		if prior.Address == batch.Address && prior.Storage == batch.Storage && prior.Versions == batch.Versions {
			return batches
		}
	}
	return append(batches[:len(batches):len(batches)], batch)
}

func heapDiscardDifference(batches, keep []session.HeapDiscard) []session.HeapDiscard {
	if len(keep) == 0 {
		return batches
	}
	var lost []session.HeapDiscard
	for _, batch := range batches {
		known := false
		for _, prior := range keep {
			if prior.Address == batch.Address && prior.Storage == batch.Storage && prior.Versions == batch.Versions {
				known = true
				break
			}
		}
		if !known {
			lost = append(lost, batch)
		}
	}
	return lost
}

// settleHeapDiscardsLocked either transfers exact debris to the surviving
// journal, or materializes it in the still-installed same heap after the owner
// loses all fences. A destructive replacement retires the old slots; neither
// name nor address coincidence authorizes moving them to its successor.
func (e *Engine) settleHeapDiscardsLocked(ref ids.TxRef, batches []session.HeapDiscard, keepJournal *session.Journal, keepWrites map[ids.ObjectAddress][]storage.RowVersion) *session.Journal {
	if len(batches) == 0 || ref.Domain() != e.Catalog.Domain || ref.Epoch() != e.Catalog.State.Epoch() {
		return keepJournal
	}
	result := keepJournal
	cloned := false
	for _, batch := range batches {
		if batch.Versions == nil || batch.Storage == nil {
			continue
		}
		if batch.Storage != e.Storage.Relations[batch.Address] && (keepJournal == nil || batch.Storage != e.PrivateStorageLocked(keepJournal, batch.Address)) {
			continue
		}
		if keepJournal != nil {
			known := false
			for _, prior := range result.DiscardedHeaps {
				if prior.Address == batch.Address && prior.Storage == batch.Storage && prior.Versions == batch.Versions {
					known = true
					break
				}
			}
			if known {
				continue
			}
			batch.Versions = storage.ExcludeOwnedDiscardedVersions(batch.Versions, batch.Storage.Versions)
			batch.Versions = storage.ExcludeOwnedDiscardedVersions(batch.Versions, keepWrites[batch.Address])
			if batch.Versions == nil {
				continue
			}
			if !cloned {
				result, cloned = keepJournal.Clone(), true
			}
			result.DiscardedHeaps = appendHeapDiscard(result.DiscardedHeaps, batch)
			continue
		}
		batch.Versions = storage.ExcludeOwnedDiscardedVersions(batch.Versions, batch.Storage.Versions)
		retained, changed := storage.MergeDiscardedVersions(batch.Storage.Discarded, batch.Versions, keepWrites[batch.Address])
		if changed {
			batch.Storage.Discarded = retained
			batch.Storage.Generation++
		}
	}
	return result
}

func (e *Engine) discardWritesLocked(ref ids.TxRef, journal *session.Journal, rows, keep map[ids.ObjectAddress][]storage.RowVersion, keepJournal *session.Journal, batches []session.HeapDiscard) *session.Journal {
	result := e.settleHeapDiscardsLocked(ref, batches, keepJournal, keep)
	if journal != nil {
		result = e.settleHeapDiscardsLocked(ref, journal.DiscardedHeaps, result, keep)
	}
	for address, versions := range rows {
		batch := e.captureDiscardedRowsLocked(ref, journal, address, versions, keep[address])
		if batch.Versions != nil {
			one := [1]session.HeapDiscard{batch}
			result = e.settleHeapDiscardsLocked(ref, one[:], result, keep)
		}
	}
	return result
}

// deferProjectedDiscardsLocked runs after merge validation, before ownership
// transfer. Projected roots may retire existing owner rows without carrying
// them as successors' PrivateHistory. The new journal owns those exact headers.
func (e *Engine) deferProjectedDiscardsLocked(t *session.TransactionOwner, next *session.Journal, writes map[ids.ObjectAddress][]storage.RowVersion) *session.Journal {
	result := next
	cloned := false
	for address, rows := range t.Writes {
		batch := e.captureDiscardedRowsLocked(t.Ref, t.Catalog, address, rows, writes[address])
		if batch.Versions == nil {
			continue
		}
		if result == nil {
			one := [1]session.HeapDiscard{batch}
			e.settleHeapDiscardsLocked(t.Ref, one[:], nil, writes)
			continue
		}
		if !cloned {
			result, cloned = next.Clone(), true
		}
		result.DiscardedHeaps = appendHeapDiscard(result.DiscardedHeaps, batch)
	}
	return result
}
