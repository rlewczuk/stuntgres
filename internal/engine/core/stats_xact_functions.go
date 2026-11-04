package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
)

// XactFunctionStatistics copies admitted current-backend pending activity. It
// never consults a cumulative Reader or rollback-restored tuple ledger.
func (r *catalogRuntime) XactFunctionStatistics(address ids.ObjectAddress) (pgcatalog.XactFunctionFact, bool) {
	s := r.auth.Statement
	if r.auth.Session == nil || PayloadOf(r.auth.Session) == nil || s == nil || s.Op == nil || s.Op.Session != r.auth.Session || s.Owner == nil || s.Engine() != r.engine || r.auth.Catalog.Owner() != &r.engine.Catalog.Owner {
		return pgcatalog.XactFunctionFact{}, false
	}
	if _, ok := r.auth.Catalog.Procedure(address); !ok {
		return pgcatalog.XactFunctionFact{}, false
	}
	key := statistics.Key{Object: r.engine.Catalog.Domain.Ref(r.auth.Catalog.Epoch(), address), Category: statistics.Function}
	f, present := PayloadOf(r.auth.Session).statsWriter.PendingFunction(&r.engine.Statistics, key)
	return pgcatalog.XactFunctionFact{Calls: f.Calls, LogicalTotal: f.FunctionLogicalTotal, LogicalSelf: f.FunctionLogicalSelf}, present
}
