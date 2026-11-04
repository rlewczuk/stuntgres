package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"time"
)

// FunctionStatistics reads membership and counters from one cumulative cut.
// It never publishes pending backend activity or changes collection policy.
func (r *catalogRuntime) FunctionStatistics(address ids.ObjectAddress) (pgcatalog.FunctionFact, bool) {
	s := r.auth.Statement
	if r.auth.Session == nil || PayloadOf(r.auth.Session) == nil || s == nil || s.Op == nil || s.Op.Session != r.auth.Session || s.Owner == nil || s.Engine() != r.engine || r.auth.Catalog.Owner() != &r.engine.Catalog.Owner {
		return pgcatalog.FunctionFact{}, false
	}
	if _, ok := r.auth.Catalog.Procedure(address); !ok {
		return pgcatalog.FunctionFact{}, false
	}
	settings := r.settings
	if settings == nil { settings = r.auth.Session.Settings }
	reader := statsReader(r.auth.Session, settings)
	key := statistics.Key{Object: r.engine.Catalog.Domain.Ref(r.auth.Catalog.Epoch(), address), Category: statistics.Function}
	captureDatabaseStats(r.engine, reader)
	f, present := reader.ReadEntry(&r.engine.Statistics, key, time.Time{})
	return pgcatalog.FunctionFact{Calls: f.Calls, LogicalTotal: f.FunctionLogicalTotal, LogicalSelf: f.FunctionLogicalSelf}, present
}
