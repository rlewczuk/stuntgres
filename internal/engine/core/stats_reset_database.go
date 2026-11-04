package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"time"
)

// Current routine admission precedes publication. Engine.Mu serializes actual
// cluster/catalog authority; provider locks are leaves. No Writer or Reader is
// borrowed, flushed, cleared or retained by a SQL reset.
func (h *statementHost) StatsResetDatabase(kind functions.StatsResetDatabaseKind, argument sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if s == nil || s.Op == nil || s.Op.Session == nil || result == nil || result.ID() != sqltype.Void || kind.Name() == "" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid database statistics reset authority")
	}
	if kind != functions.ResetDatabase && (argument.Type() == nil || argument.Type().ID() != sqltype.Oid) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid object statistics reset authority")
	}
	if err := h.Context().Err(); err != nil {
		return sqltype.Value{}, err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if _, err := s.RoutineAuthorizationLocked(); err != nil {
		return sqltype.Value{}, err
	}
	if kind != functions.ResetDatabase && argument.IsNull() {
		return sqltype.Null(result), nil
	}
	// Engine/cluster authority is already held; CatalogRead would lock it again.
	read := e.Catalog.Current()
	at := time.Now().UTC().Truncate(time.Microsecond)
	database := databaseStatsKey(e)
	if kind == functions.ResetDatabase {
		var storage [16]ids.OID
		shared := storage[:0]
		for address, relation := range read.Relations() {
			if catalog.SharedRelation(relation) {
				shared = append(shared, address.ObjectID)
			}
		}
		e.Statistics.ResetDatabase(database, read.Epoch(), shared, at)
		return sqltype.VoidValue(result), nil
	}
	oid, err := argument.OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	category, class := statistics.Relation, ids.RelationClassID
	if kind == functions.ResetFunctionCounters {
		category, class = statistics.Function, ids.ProcedureClassID
	}
	address := ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(oid)}
	if kind == functions.ResetTableCounters {
		if relation, exists := read.Relation(address); exists && catalog.SharedRelation(relation) {
			defaultTarget := e
			if e.Cluster != nil {
				defaultTarget = e.Cluster.DefaultTarget
			}
			reset := func(target *Engine) {
				var database *statistics.Key
				shared := sharedDatabaseStatsKey(target)
				if target == defaultTarget {
					database = &shared
				}
				target.Statistics.ResetObject(target.Catalog.Domain.Ref(target.Catalog.Current().Epoch(), address), statistics.Relation, database, at)
			}
			reset(e)
			if e.Cluster != nil {
				for _, target := range e.Cluster.Targets {
					if target != e {
						reset(target)
					}
				}
			}
			return sqltype.VoidValue(result), nil
		}
	}
	e.Statistics.ResetObject(e.Catalog.Domain.Ref(read.Epoch(), address), category, &database, at)
	return sqltype.VoidValue(result), nil
}
