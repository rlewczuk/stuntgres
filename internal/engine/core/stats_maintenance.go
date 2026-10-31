package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"time"
)

// RecordStatsMaintenance reports successful work without governing its physical
// storage or optimizer publication. Engine.Mu and transaction admission are held.
func (s *StatementScope) RecordStatsMaintenance(ref ids.ObjectRef, analyze bool, started, completed time.Time, live, dead int64, resetMods bool) {
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	if !policy.Counts {
		return
	}
	e := s.Engine()
	read := e.PrivateCatalogLocked(s.PendingCatalog())
	relation, ok := read.Relation(ref.Address)
	if !ok || e.Catalog.Domain.Ref(read.Epoch(), ref.Address) != ref {
		return
	}
	key := statistics.Key{Object: ref, Category: statistics.Relation}
	if analyze {
		// Partitioned parents have no local tuple accounting to subtract.
		if relation.RelationKind != catalog.PartitionedRelation {
			counts := s.Owner.Stats.CurrentCounts(key, s.Stage.Stats)
			live = max(live-(counts.Inserts-counts.Deletes), 0)
			dead = max(dead-counts.Updates-counts.Deletes-PayloadOf(s.Op.Session).statsWriter.PendingDead(&e.Statistics, key), 0)
		}
		e.Statistics.ReportAnalyze(key, policy, completed, time.Since(started), live, dead, resetMods)
	} else {
		e.Statistics.ReportVacuum(key, policy, completed, time.Since(started), live, dead)
	}
}
func (s *StatementScope) recordStatsTruncate(ref ids.ObjectRef) {
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	if !policy.Counts {
		return
	}
	key := statistics.Key{Object: ref, Category: statistics.Relation}
	s.Stage.Stats.Record(key, statistics.Fact{Truncated: true}, len(s.Owner.Savepoints))
}

// statsObjectCategory maps only the catalog identities owned by these collectors.
func statsObjectCategory(address ids.ObjectAddress) (statistics.Category, bool) {
	switch address.ClassID {
	case ids.RelationClassID:
		return statistics.Relation, true
	case ids.ProcedureClassID:
		return statistics.Function, true
	default:
		return 0, false
	}
}

// Abort retires journal-proven creations discarded by this boundary; commit
// retires explicit drops, including existing objects. A missing descriptor entry
// is never a drop. Restored owner/statement roots retain creation ownership.
func retireStatsObjects(e *Engine, sess *session.Session, epoch ids.CatalogEpoch, committed bool, discarded *session.Journal, retained ...*session.Journal) {
	if discarded == nil {
		return
	}
	for address, original := range discarded.Originals {
		category, tracked := statsObjectCategory(address)
		if !tracked {
			continue
		}
		descriptor, present := discarded.Descriptors[address]
		if !present || committed && descriptor != nil || !committed && original != nil {
			continue
		}
		if !committed {
			keep := false
			for _, journal := range retained {
				if journal != nil {
					if before, owned := journal.Originals[address]; owned && before == nil {
						keep = true
						break
					}
				}
			}
			if keep {
				continue
			}
		}
		PayloadOf(sess).statsWriter.Retire(&e.Statistics, statistics.Key{Object: e.Catalog.Domain.Ref(epoch, address), Category: category})
	}
}
