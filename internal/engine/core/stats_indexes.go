package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"time"
)

// Transaction admission serializes this physical writer with the idle drain.
// A failed top-level block retains its identity until ROLLBACK; stamp it once.
func statsTransactionStopped(t *session.TransactionOwner) {
	p := PayloadOf(t.Session)
	if p == nil || t.Ref == (ids.TxRef{}) || p.statsStopped == t.Ref {
		return
	}
	p.statsStopped = t.Ref
	p.statsWriter.TransactionStopped(time.Now().UTC().Truncate(time.Microsecond))
}

func (h *statementHost) CaptureIndexAccess(ref ids.ObjectRef) statistics.IndexAccess {
	if h == nil || h.Op == nil || h.Op.Session == nil {
		return statistics.IndexAccess{}
	}
	p := PayloadOf(h.Op.Session)
	if p == nil {
		return statistics.IndexAccess{}
	}
	_, _, policy := h.Op.EffectiveSettings().StatsConfiguration()
	if !policy.Counts {
		return statistics.IndexAccess{}
	}
	return p.statsWriter.CaptureIndexAccess(&h.scope().Engine().Statistics, statistics.Key{Object: ref, Category: statistics.Relation}, policy)
}

func (h *statementHost) CaptureRelationAccess(ref ids.ObjectRef) statistics.RelationAccess {
	if h == nil || h.Op == nil || h.Op.Session == nil {
		return statistics.RelationAccess{}
	}
	p := PayloadOf(h.Op.Session)
	if p == nil {
		return statistics.RelationAccess{}
	}
	_, _, policy := h.Op.EffectiveSettings().StatsConfiguration()
	if !policy.Counts {
		return statistics.RelationAccess{}
	}
	return p.statsWriter.CaptureRelationAccess(&h.scope().Engine().Statistics, statistics.Key{Object: ref, Category: statistics.Relation}, policy)
}

func (r *catalogRuntime) IndexStatistics(address ids.ObjectAddress) pgcatalog.IndexStatisticsFact {
	if r.auth.Session == nil || PayloadOf(r.auth.Session) == nil {
		return pgcatalog.IndexStatisticsFact{}
	}
	settings := r.settings
	if settings == nil {
		settings = r.auth.Session.Settings
	}
	reader := statsReader(r.auth.Session, settings)
	var now time.Time
	if reader.NeedsSnapshot() {
		now = time.Now().UTC().Truncate(time.Microsecond)
	}
	f := reader.Read(&r.engine.Statistics, statistics.Key{Object: r.engine.Catalog.Domain.Ref(r.auth.Catalog.Epoch(), address), Category: statistics.Relation}, now)
	return pgcatalog.IndexStatisticsFact{Scans: f.IndexScans, Entries: f.IndexEntries, Fetches: f.IndexFetches, LastScan: f.LastIndexScan}
}
