package core

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) StatsXactBlocksValue(kind functions.StatsXactBlocksKind, oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if s == nil || s.Op == nil || s.Op.Session == nil || result == nil || result.ID() != sqltype.Int8 || kind.Name() == "" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid transaction block inquiry authority")
	}
	if err := s.Active(); err != nil {
		return sqltype.Value{}, err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	payload := PayloadOf(s.Op.Session)
	view := e.Catalog.Current()
	database, live := view.Database(e.Catalog.DatabaseAddress())
	if payload == nil || EngineOf(s.Op.Session) != e || e.Cluster == nil || !live || e.Cluster.Targets[database.Name()] != e {
		return sqltype.Value{}, fmt.Errorf("stuntgres: detached transaction block inquiry owner")
	}
	// Numeric lookup deliberately does not resolve/open the relation, check its
	// SELECT privilege, construct a Reader cut, sample a clock or publish work.
	key := statistics.Key{Object: e.Catalog.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}), Category: statistics.Relation}
	fetched, hit := payload.statsWriter.PendingBlocks(&e.Statistics, key)
	if kind == functions.XactBlocksHit {
		return sqltype.Integer(result, hit)
	}
	return sqltype.Integer(result, fetched)
}
