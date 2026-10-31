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
func (r EngineCatalogRuntime) TableStatistics(address ids.ObjectAddress) pgcatalog.MaintenanceFact {
	if r.Auth.Session == nil || PayloadOf(r.Auth.Session) == nil {
		return pgcatalog.MaintenanceFact{}
	}
	if r.settings == nil {
		r.settings = r.Auth.Session.Settings
	}
	reader := statsReader(r.Auth.Session, r.settings)
	key := statistics.Key{Object: r.engine.Catalog.Domain.Ref(r.Auth.Catalog.Epoch(), address), Category: statistics.Relation}
	var now time.Time
	if reader.NeedsSnapshot() {
		now = time.Now().UTC().Truncate(time.Microsecond)
	}
	f := reader.Read(&r.engine.Statistics, key, now)
	return pgcatalog.MaintenanceFact{Inserts: f.Inserts, Updates: f.Updates, Deletes: f.Deletes, Live: f.Live, Dead: f.Dead, Modifications: f.Modifications, InsertsSince: f.InsertsSince, VacuumCount: f.VacuumCount, LastVacuum: f.LastVacuum, AnalyzeCount: f.AnalyzeCount, LastAnalyze: f.LastAnalyze, VacuumTime: f.VacuumTime, AnalyzeTime: f.AnalyzeTime}
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
			e.Statistics.Command(s.SessionToken, ids.CommandIdentity{}, "", time.Time{}, false)
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
	e.Statistics.Idle(s.SessionToken)
}
func (s *StatementScope) statsCommand() {
	if s.Op == nil || s.Op.Session == nil || s.Op.EffectiveSettings() == nil || s.Engine() == nil {
		return
	}
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	s.Engine().Statistics.Command(s.Op.Session.SessionToken, ids.CommandIdentity{Transaction: s.TxRef, Command: s.CommandID}, s.Op.OriginalSQL, s.statementStart, policy.Activities)
}
