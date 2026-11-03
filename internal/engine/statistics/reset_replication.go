package statistics

import (
 "time"
 "stuntgres/internal/engine/ids"
)

func perEntryReset(category Category) bool { return category==ReplicationSlot || category==Subscription }
func missingFact(category Category, reset time.Time, generation uint64) Fact {
 if perEntryReset(category) { reset=time.Time{} }
 return Fact{ResetAt:reset,Generation:generation}
}
// EnsureSubscription is called only for an actual newly created subscription.
// Reset and reads never allocate a missing record or remove a tombstone.
func (p *Provider) EnsureSubscription(key Key) {
 if key.Category!=Subscription { return }
 p.mu.Lock(); defer p.mu.Unlock()
 if _,retired:=p.retired[key]; retired { return }
 if _,exists:=p.facts[key]; exists { return }
 if p.facts==nil { p.facts=make(map[Key]Fact) }
 p.facts[key]=Fact{}
}
func resetReplicationFact(category Category, f Fact, at time.Time) Fact {
 switch category {
 case ReplicationSlot:
  f.SpillTxns,f.SpillCount,f.SpillBytes=0,0,0
  f.StreamTxns,f.StreamCount,f.StreamBytes=0,0,0
  f.TotalTxns,f.TotalBytes=0,0
 case Subscription:
  f.ApplyErrorCount,f.SyncErrorCount=0,0
  f.ConflInsertExists,f.ConflUpdateOriginDiffers,f.ConflUpdateExists,f.ConflUpdateMissing=0,0,0,0
  f.ConflDeleteOriginDiffers,f.ConflDeleteMissing,f.ConflMultipleUniqueConflicts=0,0,0
 default: return f
 }
 f.ResetAt=at
 return f
}
// ResetEntry changes existing shared counters only; generation, pending batches,
// cached readers, global reset timestamp and retirement authority are unchanged.
func (p *Provider) ResetEntry(key Key, at time.Time) bool {
 if !perEntryReset(key.Category) { return false }
 p.mu.Lock(); defer p.mu.Unlock()
 f,exists:=p.facts[key]; if !exists { return false }
 p.facts[key]=resetReplicationFact(key.Category,f,at)
 return true
}

// ResetSubscriptionOID searches existing shared records, not catalog visibility.
// The stored full key retains the actual owning domain and catalog incarnation,
// including a provisional creation or an entry awaiting committed DROP.
func (p *Provider) ResetSubscriptionOID(oid ids.OID, at time.Time) bool {
 address:=ids.ObjectAddress{ClassID:ids.SubscriptionClassID,ObjectID:oid}
 p.mu.Lock(); defer p.mu.Unlock()
 for key,fact:=range p.facts {
  if key.Category!=Subscription || key.Object.Address!=address { continue }
  if _,retired:=p.retired[key]; retired { continue }
  p.facts[key]=resetReplicationFact(Subscription,fact,at)
  return true
 }
 return false
}
func (p *Provider) ResetCategory(category Category, at time.Time) {
 if !perEntryReset(category) { return }
 p.mu.Lock(); defer p.mu.Unlock()
 for key,f:=range p.facts { if key.Category==category { p.facts[key]=resetReplicationFact(category,f,at) } }
}

func mergeCategory(category Category, a, b Fact) Fact {
	switch category {
	case ReplicationSlot:
		a.SpillTxns += b.SpillTxns
		a.SpillCount += b.SpillCount
		a.SpillBytes += b.SpillBytes
		a.StreamTxns += b.StreamTxns
		a.StreamCount += b.StreamCount
		a.StreamBytes += b.StreamBytes
		a.TotalTxns += b.TotalTxns
		a.TotalBytes += b.TotalBytes
	case Subscription:
		a.ApplyErrorCount += b.ApplyErrorCount
		a.SyncErrorCount += b.SyncErrorCount
		a.ConflInsertExists += b.ConflInsertExists
		a.ConflUpdateOriginDiffers += b.ConflUpdateOriginDiffers
		a.ConflUpdateExists += b.ConflUpdateExists
		a.ConflUpdateMissing += b.ConflUpdateMissing
		a.ConflDeleteOriginDiffers += b.ConflDeleteOriginDiffers
		a.ConflDeleteMissing += b.ConflDeleteMissing
		a.ConflMultipleUniqueConflicts += b.ConflMultipleUniqueConflicts
	default:
		return merge(a, b)
	}
	return a
}
