package core

import (
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"time"
)

func databaseStatsKey(e *Engine) statistics.Key {
	return statistics.Key{Object: e.Catalog.Domain.Ref(0, e.Catalog.DatabaseAddress()), Category: statistics.Database}
}
func addDatabaseStats(e *Engine, s *session.Session, fact statistics.DatabaseFact) {
	if fact == (statistics.DatabaseFact{}) {
		return
	}
	if p := PayloadOf(s); p != nil && p.statsPublic {
		p.statsWriter.Add(&e.Statistics, databaseStatsKey(e), statistics.Fact{Database: fact})
	}
}

// Every cumulative first-access authority calls this with the shared engine
// mutex held, before a keyed or fixed-family read can mark a snapshot complete.
// The same cut retains peer database/subscription facts and owning defaults.
// Timestamp/clear, activity and uncached recovery-prefetch reads stay lazy.
func captureDatabaseStats(e *Engine, reader *statistics.Reader) {
	if !reader.NeedsSnapshot() {
		return
	}
	targets := make([]*Engine, 0, len(e.Cluster.Targets))
	for _, target := range e.Cluster.Targets {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Catalog.DatabaseAddress().ObjectID < targets[j].Catalog.DatabaseAddress().ObjectID
	})
	sources := make([]statistics.CaptureSource, 0, len(targets))
	shared := e.Cluster.DefaultTarget
	sharedCaptured := false
	for _, target := range targets {
		source := statistics.CaptureSource{Provider: &target.Statistics, Database: databaseStatsKey(target), All: target == e}
		// The registry carries immutable public identity. Mirrors grant reporting
		// authority but never replace an owning provider's captured backend fact.
		target.sessionMu.Lock()
		for token, session := range target.sessions {
			if p := PayloadOf(session); p != nil && p.statsPublic {
				if activity, present := e.activityProvider().LiveActivity(token); present {
					source.Owners = append(source.Owners, statistics.BackendIdentity{Token: token, Incarnation: activity.Incarnation})
				}
			}
		}
		target.sessionMu.Unlock()
		if target == shared {
			source.Shared = sharedDatabaseStatsKey(shared)
			sharedCaptured = true
		}
		sources = append(sources, source)
	}
	if !sharedCaptured {
		sources = append(sources, statistics.CaptureSource{Provider: &shared.Statistics, Database: sharedDatabaseStatsKey(shared)})
	}
	reader.Capture(sources, time.Now().UTC().Truncate(time.Microsecond))
}

func (r *catalogRuntime) DatabaseStatistics(oid ids.OID) pgcatalog.DatabaseStatisticsFact {
	// Standalone runtimes have no synchronous capture borrow. Keep membership
	// authority through both the cumulative cut and live backend enumeration.
	if !r.engineLocked {
		r.engine.Mu.Lock()
		defer r.engine.Mu.Unlock()
	}
	var result pgcatalog.DatabaseStatisticsFact
	settings := r.settings
	if settings == nil {
		settings = r.auth.Session.Settings
	}
	settings.Mu.Lock()
	result.Checksums = settings.TypedValue("data_checksums").Boolean
	settings.Mu.Unlock()
	reader := statsReader(r.auth.Session, settings)
	captureDatabaseStats(r.engine, reader)
	if oid == 0 {
		shared := r.engine.Cluster.DefaultTarget
		result.Cumulative = reader.Read(&shared.Statistics, sharedDatabaseStatsKey(shared), time.Time{}).Database
		return result
	}
	for _, target := range r.engine.Cluster.Targets {
		if target.Catalog.DatabaseAddress().ObjectID != oid {
			continue
		}
		result.Cumulative = reader.Read(&target.Statistics, databaseStatsKey(target), time.Time{}).Database
		target.sessionMu.Lock()
		for _, s := range target.sessions {
			// Immutable publication identity: never acquire Session.Mu under sessionMu.
			if p := PayloadOf(s); p != nil && p.statsPublic {
				result.Backends++
			}
		}
		target.sessionMu.Unlock()
		break
	}
	return result
}

func statsTransactionOutcome(t *session.TransactionOwner, committed bool) {
	p := PayloadOf(t.Session)
	if p == nil || t.Ref == (ids.TxRef{}) || p.statsOutcome == t.Ref {
		return
	}
	p.statsOutcome = t.Ref
	fact := statistics.DatabaseFact{Rollbacks: 1}
	if committed {
		fact.Commits, fact.Rollbacks = 1, 0
	}
	addDatabaseStats(EngineOf(t.Session), t.Session, fact)
}

func (h *statementHost) DatabaseStatisticsPolicy() statistics.Policy {
	// Captured metadata-only/native sources may carry a scope without a live
	// operation. They have no session-owned collection authority.
	if h == nil || h.Op == nil || h.Op.Session == nil || h.Op.EffectiveSettings() == nil {
		return statistics.Policy{}
	}
	s := h.scope()
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	return policy
}
func (h *statementHost) RecordDatabaseStatistics(fact statistics.DatabaseFact) {
	if h == nil || h.Op == nil || h.Op.Session == nil {
		return
	}
	s := h.scope()
	addDatabaseStats(s.Engine(), s.Op.Session, fact)
}

type statsWriteUsage struct {
	started time.Time
	counts  bool
}

func (s *StatementScope) beginDatabaseWrite() statsWriteUsage {
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	usage := statsWriteUsage{counts: policy.Counts}
	if policy.Counts && policy.IOTiming {
		usage.started = time.Now()
	}
	return usage
}
func (s *StatementScope) finishDatabaseWrite(usage statsWriteUsage) {
	if !usage.started.IsZero() {
		addDatabaseStats(s.Engine(), s.Op.Session, statistics.DatabaseFact{WriteTime: time.Since(usage.started)})
	}
}

func sharedDatabaseStatsKey(e *Engine) statistics.Key {
	return statistics.Key{Object: e.Catalog.Domain.Ref(0, ids.ObjectAddress{ClassID: ids.DatabaseClassID}), Category: statistics.Database}
}

// A real accepted standalone control/SQL-processing attempt can complete a
// backend command transaction without owning a visibility TransactionOwner.Ref.
// Its scope, not error text or later cleanup, is the once-only event authority.
func (s *StatementScope) statsControlOutcome(committed bool) {
	if s.statsCommandOutcome {
		return
	}
	s.statsCommandOutcome = true
	fact := statistics.DatabaseFact{Rollbacks: 1}
	if committed {
		fact.Commits, fact.Rollbacks = 1, 0
	}
	addDatabaseStats(s.Engine(), s.Op.Session, fact)
}

// AcceptEmptySQL marks a successfully parsed empty simple-query message, not
// Describe/Prepare or idle physical cleanup. An explicit transaction stays open.
func (s *StatementScope) AcceptEmptySQL() {
	if s.statsIdleCommand && s.Owner.State == session.TxIdle && s.Op.SQLAvailable {
		s.statsControlOutcome(true)
	}
}
