package core

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"time"
)

func statsReader(s *session.Session, settings *session.SettingsStore) *statistics.Reader {
	p := PayloadOf(s)
	mode, generation, _ := settings.StatsConfiguration()
	p.statsReader.Configure(mode, generation)
	return &p.statsReader
}
func (h *statementHost) StatsSnapshotValue(clear bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if s == nil || s.Op == nil || s.Op.Session == nil || PayloadOf(s.Op.Session) == nil || s.Op.EffectiveSettings() == nil || result == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid statistics snapshot authority")
	}
	reader := statsReader(s.Op.Session, s.Op.EffectiveSettings())
	if clear && result.ID() == sqltype.Void {
		reader.Clear()
		return sqltype.VoidValue(result), nil
	}
	if !clear && result.ID() == sqltype.Timestamptz {
		at := reader.Timestamp()
		if at.IsZero() {
			return sqltype.Null(result), nil
		}
		return sqltype.TimestampFromTime(result, at)
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid statistics snapshot runtime signature")
}
func (r *catalogRuntime) relationStatistics(address ids.ObjectAddress) statistics.Fact {
	if r.auth.Session == nil || PayloadOf(r.auth.Session) == nil {
		return statistics.Fact{}
	}
	settings := r.settings
	if settings == nil {
		settings = r.auth.Session.Settings
	}
	reader := statsReader(r.auth.Session, settings)
	key := statistics.Key{Object: r.engine.Catalog.Domain.Ref(r.auth.Catalog.Epoch(), address), Category: statistics.Relation}
	captureDatabaseStats(r.engine, reader)
	return reader.Read(&r.engine.Statistics, key, time.Time{})
}
func (r *catalogRuntime) TableBlockStatistics(address ids.ObjectAddress) pgcatalog.IndexBlockFact {
	f := r.relationStatistics(address)
	return pgcatalog.IndexBlockFact{Read: f.BlocksFetched - f.BlocksHit, Hit: f.BlocksHit}
}
func (r *catalogRuntime) IndexBlockStatistics(address ids.ObjectAddress) pgcatalog.IndexBlockFact {
	f := r.relationStatistics(address)
	return pgcatalog.IndexBlockFact{Read: f.IndexBlocksRead, Hit: f.IndexBlocksHit}
}
func (r *catalogRuntime) TableStatistics(address ids.ObjectAddress) pgcatalog.MaintenanceFact {
	f := r.relationStatistics(address)
	return pgcatalog.MaintenanceFact{SeqScans: f.SeqScans, SeqTuples: f.SeqTuples, LastSeqScan: f.LastSeqScan, TableIndexFetches: f.TableIndexFetches, Inserts: f.Inserts, Updates: f.Updates, Deletes: f.Deletes, Live: f.Live, Dead: f.Dead, Modifications: f.Modifications, InsertsSince: f.InsertsSince, VacuumCount: f.VacuumCount, LastVacuum: f.LastVacuum, AnalyzeCount: f.AnalyzeCount, LastAnalyze: f.LastAnalyze, VacuumTime: f.VacuumTime, AnalyzeTime: f.AnalyzeTime}
}

func (r *catalogRuntime) SequenceIOStatistics(address ids.ObjectAddress) int64 {
	return r.relationStatistics(address).SequencePageAccesses
}

func (r *catalogRuntime) IOStatisticsReset() time.Time {
	if r.auth.Session == nil || PayloadOf(r.auth.Session) == nil {
		return time.Time{}
	}
	settings := r.settings
	if settings == nil {
		settings = r.auth.Session.Settings
	}
	reader := statsReader(r.auth.Session, settings)
	if reader.NeedsSnapshot() {
		// IO sources run unlocked; cluster snapshot membership requires the
		// shared engine mutex before any fixed-family read marks it complete.
		r.engine.Mu.Lock()
		captureDatabaseStats(r.engine, reader)
		r.engine.Mu.Unlock()
	}
	return reader.ReadShared(&r.engine.Statistics, statistics.SharedIO, time.Time{}).ResetAt
}

// recordStatsTuple is called at real accepted row mutation, not at COMMIT.
// Engine and transaction owner locks are held. Estimate ownership stays beside
// writes; collecting a tuple never creates a catalog journal or changes routing.
func (s *StatementScope) recordStatsTuple(ref ids.ObjectRef, inserts, updates, deletes int64, counts bool) {
	if !counts {
		return
	}
	key := statistics.Key{Object: ref, Category: statistics.Relation}
	s.Stage.Stats.Record(key, statistics.Fact{Inserts: inserts, Updates: updates, Deletes: deletes, Live: inserts - deletes, Dead: updates + deletes, Modifications: inserts + updates + deletes, InsertsSince: inserts}, len(s.Owner.Savepoints))
}
func statsIdle(s *session.Session, force bool) {
	p := PayloadOf(s)
	if p == nil {
		return
	}
	e := EngineOf(s)
	if e == nil {
		return
	}
	if s.Transactions != nil {
		addDatabaseStats(e, s, statistics.DatabaseFact{Deadlocks: s.Transactions.Ticket.TakeDeadlocks()})
	}
	addDatabaseStats(e, s, statistics.DatabaseFact{Deadlocks: s.Advisory.Ticket.TakeDeadlocks()})
	if !force && s.Settings != nil {
		_, _, policy := s.Settings.StatsConfiguration()
		if !policy.Activities {
			e.activityProvider().Command(s.SessionToken, ids.CommandIdentity{}, "", time.Time{}, false)
		}
	}
	if force {
		if p.statsPublic {
			addDatabaseStats(e, s, p.statsSession.Transition(time.Now(), false, false, false))
			switch s.DisconnectCause {
			case session.DisconnectAbandoned:
				addDatabaseStats(e, s, statistics.DatabaseFact{Abandoned: 1})
			case session.DisconnectFatal:
				addDatabaseStats(e, s, statistics.DatabaseFact{Fatal: 1})
			case session.DisconnectKilled:
				addDatabaseStats(e, s, statistics.DatabaseFact{Killed: 1})
			}
		}
		// Unregister holds Session.Mu after the outside-lock callback drain.
		p.statsFlush.stop()
		if p.statsWriter.Pending() {
			p.statsWriter.Flush(&e.Statistics, time.Now(), true)
		}
	} else {
		_, _, policy := s.Settings.StatsConfiguration()
		s.Mu.Lock()
		if s.State == session.Live && p.statsPublic {
			addDatabaseStats(e, s, p.statsSession.Transition(time.Now(), false, s.Transactions != nil && s.Transactions.State != session.TxIdle, policy.Activities))
		}
		if s.State == session.Live && statsOutsideTransaction(s) {
			if p.statsWriter.Pending() {
				statsReportIdleLocked(s, time.Now())
			} else {
				statsReportIdleLocked(s, time.Time{})
			}
		}
		s.Mu.Unlock()
	}
	if !force {
		activityIdle(s)
	}
}
func (s *StatementScope) statsCommand() {
	if s.Op == nil || s.Op.Session == nil || s.Op.EffectiveSettings() == nil || s.Engine() == nil {
		return
	}
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	s.LockTicket.SetStatisticsCounts(policy.Counts)
	s.Op.Session.Advisory.Ticket.SetStatisticsCounts(policy.Counts)
	if p := PayloadOf(s.Op.Session); p != nil && p.statsPublic {
		addDatabaseStats(s.Engine(), s.Op.Session, p.statsSession.Transition(time.Now(), true, false, policy.Activities))
	}
	if !s.Op.CommandAdmission {
		return
	}
	provider := s.Engine().activityProvider()
	if s.statementStatsDepth == 0 {
		s.activityCommand = ids.CommandIdentity{Transaction: s.TxRef, Command: s.CommandID}
	} else if s.TxRef == (ids.TxRef{}) || s.activityCommand.Transaction == s.TxRef {
		return
	} else {
		// A real nested transaction-control successor changes association,
		// not the frontend source, start or computed top-level identifier.
		s.activityCommand.Transaction = s.TxRef
	}
	provider.Command(s.Op.Session.SessionToken, s.activityCommandIdentity(), s.Op.OriginalSQL, s.statementStart, policy.Activities)
	if s.TxRef != (ids.TxRef{}) {
		provider.ActivityTransaction(s.Op.Session.SessionToken, s.activityCommandIdentity(), s.Owner.Start)
	}
}
