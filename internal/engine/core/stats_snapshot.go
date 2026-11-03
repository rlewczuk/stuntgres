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
	var now time.Time
	if reader.NeedsSnapshot() {
		now = time.Now().UTC().Truncate(time.Microsecond)
	}
	return reader.Read(&r.engine.Statistics, key, now)
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
	if r.auth.Session == nil || PayloadOf(r.auth.Session) == nil {
		return 0
	}
	settings := r.settings
	if settings == nil {
		settings = r.auth.Session.Settings
	}
	reader := statsReader(r.auth.Session, settings)
	key := statistics.Key{Object: r.engine.Catalog.Domain.Ref(r.auth.Catalog.Epoch(), address), Category: statistics.Relation}
	var now time.Time
	if reader.NeedsSnapshot() {
		now = time.Now().UTC().Truncate(time.Microsecond)
	}
	return reader.Read(&r.engine.Statistics, key, now).SequencePageAccesses
}

// recordStatsTuple is called at real accepted row mutation, not at COMMIT.
// Engine and transaction owner locks are held. Estimate ownership stays beside
// writes; collecting a tuple never creates a catalog journal or changes routing.
func (s *StatementScope) recordStatsTuple(ref ids.ObjectRef, inserts, updates, deletes int64) {
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	if !policy.Counts {
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
	if !force && s.Settings != nil {
		_, _, policy := s.Settings.StatsConfiguration()
		if !policy.Activities {
			e.activityProvider().Command(s.SessionToken, ids.CommandIdentity{}, "", time.Time{}, false)
		}
	}
	if force {
		// Unregister holds Session.Mu after the outside-lock callback drain.
		p.statsFlush.stop()
		if p.statsWriter.Pending() {
			p.statsWriter.Flush(&e.Statistics, time.Now(), true)
		}
	} else {
		s.Mu.Lock()
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
