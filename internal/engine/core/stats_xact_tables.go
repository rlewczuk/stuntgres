package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
)

// XactRelationStatistics runs under statement admission and Engine.Mu. Access
// events already belong to the physical writer; only unresolved tuple events
// need the current owner ledger and its statement suffix. No cumulative reader
// is consulted, configured or populated by this inquiry.
func (r *catalogRuntime) XactRelationStatistics(address ids.ObjectAddress) pgcatalog.XactRelationFact {
	s := r.auth.Statement
	if r.auth.Session == nil || PayloadOf(r.auth.Session) == nil || s == nil || s.Op == nil || s.Op.Session != r.auth.Session || s.Owner == nil || s.Engine() != r.engine || r.auth.Catalog.Owner() != &r.engine.Catalog.Owner {
		return pgcatalog.XactRelationFact{}
	}
	if _, ok := r.auth.Catalog.Relation(address); !ok {
		return pgcatalog.XactRelationFact{}
	}
	key := statistics.Key{Object: r.engine.Catalog.Domain.Ref(r.auth.Catalog.Epoch(), address), Category: statistics.Relation}
	f := PayloadOf(r.auth.Session).statsWriter.PendingRelation(&r.engine.Statistics, key)
	counts := s.Owner.Stats.CurrentCounts(key, s.Stage.Stats)
	return pgcatalog.XactRelationFact{
		SeqScans: f.SeqScans, SeqTuples: f.SeqTuples,
		IndexScans: f.IndexScans, IndexFetches: f.IndexFetches,
		TableIndexFetches: f.TableIndexFetches,
		Inserts: f.Inserts + counts.Inserts,
		Updates: f.Updates + counts.Updates,
		Deletes: f.Deletes + counts.Deletes,
	}
}
