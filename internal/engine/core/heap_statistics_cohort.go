package core

import (
	"reflect"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Discovery never holds sessionMu while acquiring owner locks. Public IDs and,
// for ID0 owners, retained private Session pointers impose one total order in
// opposing captures. The typed owners slice keeps those sessions live throughout;
// pointer observations are ordering keys only. Engine.Mu comes last.
func (e *Engine) heapStatisticsOwners(caller *session.TransactionOwner) []*session.TransactionOwner {
	e.sessionMu.Lock()
	owners := make([]*session.TransactionOwner, 0, len(e.sessions)+1)
	found := false
	for _, s := range e.sessions {
		owners = append(owners, s.Transactions)
		found = found || s.Transactions == caller
	}
	e.sessionMu.Unlock()
	if !found {
		owners = append(owners, caller)
	}
	slices.SortFunc(owners, func(a, b *session.TransactionOwner) int {
		if a.Session.SessionToken.ID() < b.Session.SessionToken.ID() {
			return -1
		}
		if a.Session.SessionToken.ID() > b.Session.SessionToken.ID() {
			return 1
		}
		if a.Session.SessionToken.ID() == 0 {
			left, right := reflect.ValueOf(a.Session).Pointer(), reflect.ValueOf(b.Session).Pointer()
			if left < right {
				return -1
			}
			if left > right {
				return 1
			}
		}
		return 0
	})
	return owners
}

func (e *Engine) heapStatisticsCohortCurrent(owners []*session.TransactionOwner, caller *session.TransactionOwner) bool {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	count := len(e.sessions)
	if e.sessions[caller.Session.SessionToken] == nil {
		count++
	}
	if count != len(owners) {
		return false
	}
	for _, owner := range owners {
		if owner != caller && e.sessions[owner.Session.SessionToken] != owner.Session {
			return false
		}
	}
	return true
}

func (e *Engine) heapStatisticsFactsLocked(s *StatementScope, owners []*session.TransactionOwner, store *storage.Relation, address ids.ObjectAddress, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) []storage.HeapTupleFact {
	facts := make([]storage.HeapTupleFact, 0, len(store.Versions))
	positions := make(map[ids.VersionID]int, len(store.Versions))
	add := func(row storage.RowVersion, inserter ids.TxRef, end ids.CommitSeq, ender ids.TxRef) {
		row = store.Headers.Apply(row)
		if row.ForcedFrozen {
			inserter, end = ids.TxRef{}, row.ForcedEnd
		}
		if row.ForcedEnd != 0 {
			end = row.ForcedEnd
		}
		if slot, ok := positions[row.Version]; ok {
			if ender != (ids.TxRef{}) {
				facts[slot].Ender = ender
			}
			if inserter != (ids.TxRef{}) {
				facts[slot].Inserter = inserter
			}
			return
		}
		positions[row.Version] = len(facts)
		facts = append(facts, storage.HeapTupleFact{Version: row, Inserter: inserter, End: end, Ender: ender})
	}
	for _, row := range store.Versions {
		var end ids.CommitSeq
		if store.Ends != nil {
			if marker, ok := store.Ends.Lookup(row.Row, row.Version); ok {
				end = marker.End
			}
		}
		inserter := ids.TxRef{}
		if row.Born == 0 && e.Storage.Relations[address] != store {
			inserter = s.TxRef // Caller-owned new heap/REFRESH storage.
		}
		add(row, inserter, end, ids.TxRef{})
	}
	for prior := store.Retained; prior != nil; prior = prior.Next {
		add(prior.Version, ids.TxRef{}, prior.End, ids.TxRef{})
	}
	aborted := func(row storage.RowVersion) {
		add(row, ids.TxRef{}, 0, ids.TxRef{})
		if slot, ok := positions[row.Version]; ok && !facts[slot].Version.ForcedFrozen {
			facts[slot].Aborted = true
		}
	}
	for prior := store.Discarded; prior != nil; prior = prior.Next {
		aborted(prior.Version)
	}
	if store.Headers != nil {
		for _, row := range store.Headers.Additional {
			add(row, ids.TxRef{}, 0, ids.TxRef{})
		}
	}
	batches := func(values []session.HeapDiscard) {
		for _, batch := range values {
			if batch.Address == address && batch.Storage == store {
				for prior := batch.Versions; prior != nil; prior = prior.Next {
					aborted(prior.Version)
				}
			}
		}
	}
	batches(s.Stage.DiscardedHeaps)
	capture := func(ref ids.TxRef, catalog *session.Journal, rows []storage.RowVersion, deletion *visibility.PrivateDeletes) {
		if ref.Epoch() == s.TxRef.Epoch() && ref.Domain() == s.TxRef.Domain() && catalog != nil {
			batches(catalog.DiscardedHeaps)
		}
		if ref.Epoch() != s.TxRef.Epoch() || ref.Domain() != s.TxRef.Domain() || e.PrivateStorageLocked(catalog, address) != store {
			return
		}
		for _, row := range rows {
			inserter := ids.TxRef{}
			if row.Born == 0 {
				inserter = ref
			}
			add(row, inserter, 0, ids.TxRef{})
			for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
				inserter = ids.TxRef{}
				if prior.Version.Born == 0 {
					inserter = ref
				}
				ender := ref
				if header := store.Headers.Header(prior.Version.Version); header.Repaired && prior.Version.RepairStamp != header.RepairStamp {
					ender = ids.TxRef{}
				}
				add(prior.Version, inserter, prior.End, ender)
			}
		}
		if deletion != nil {
			for _, candidate := range deletion.All() {
				if candidate.Storage == store {
					if slot, ok := positions[candidate.Version]; ok && (!facts[slot].Version.ForcedFrozen || candidate.RepairStamp == facts[slot].Version.RepairStamp) {
						facts[slot].Ender = ref
					}
				}
			}
		}
	}
	seen := make(map[ids.TxRef]bool, len(owners))
	for _, owner := range owners {
		if owner.State != session.TxActive && owner.State != session.TxImplicit {
			continue
		}
		seen[owner.Ref] = true
		if owner == s.Owner {
			capture(s.TxRef, s.PendingCatalog(), writes, deletes)
		} else {
			capture(owner.Ref, owner.Catalog, owner.Writes[address], owner.Deletes[address])
		}
	}
	for _, prepared := range e.prepared.All() {
		if !seen[prepared.Ref] && prepared.Database == e.DatabaseName {
			capture(prepared.Ref, prepared.Catalog, prepared.Writes[address], prepared.Deletes[address])
		}
	}
	return facts
}

func (e *Engine) heapStatisticsPrivateWritesLocked(s *StatementScope, owners []*session.TransactionOwner, store *storage.Relation, address ids.ObjectAddress, writes []storage.RowVersion) []storage.RowVersion {
	var rows []storage.RowVersion
	seen := make(map[ids.TxRef]bool, len(owners))
	capture := func(ref ids.TxRef, journal *session.Journal, source []storage.RowVersion) {
		if !seen[ref] && ref.Epoch() == s.TxRef.Epoch() && ref.Domain() == s.TxRef.Domain() && e.PrivateStorageLocked(journal, address) == store {
			rows = append(rows, source...)
			seen[ref] = true
		}
	}
	for _, owner := range owners {
		if owner.State != session.TxActive && owner.State != session.TxImplicit {
			continue
		}
		if owner == s.Owner {
			capture(s.TxRef, s.PendingCatalog(), writes)
		} else {
			capture(owner.Ref, owner.Catalog, owner.Writes[address])
		}
	}
	for _, prepared := range e.prepared.All() {
		if prepared.Database == e.DatabaseName {
			capture(prepared.Ref, prepared.Catalog, prepared.Writes[address])
		}
	}
	return rows
}
