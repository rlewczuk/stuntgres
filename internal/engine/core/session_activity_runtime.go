package core

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

// Timestamp scalars count microseconds since PostgreSQL's 2000-01-01 UTC epoch.
const activityEpochUnixMicros = 946684800000000

func (h *statementHost) SessionActivityValue(kind functions.SessionActivityKind, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	if scope == nil || scope.Op == nil || scope.Op.Session == nil || result == nil { return sqltype.Value{}, fmt.Errorf("stuntgres: invalid session activity authority") }
	switch kind {
	case functions.ActivityQuery:
		if result.ID() != sqltype.Text { break }
		if !scope.Op.SQLAvailable { return sqltype.Null(result), nil }
		return sqltype.String(result, scope.Op.OriginalSQL), nil
	case functions.ActivityBackendPID:
		if result.ID() != sqltype.Int4 { break }
		id := scope.Op.Session.Token().ID()
		if id == 0 || id > math.MaxInt32 { return sqltype.Value{}, fmt.Errorf("stuntgres: session identity is not a positive int4") }
		return sqltype.Integer(result, int64(id))
	case functions.ActivityPostmasterStart, functions.ActivityConfigLoad:
		if result.ID() != sqltype.Timestamptz || scope.Engine().Cluster == nil { break }
		cluster := scope.Engine().Cluster
		stamp := cluster.StartedAt
		if kind == functions.ActivityConfigLoad { stamp = cluster.ConfigLoadedAt }
		return sqltype.TimestampValue(result, stamp)
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid session activity runtime signature")
}
