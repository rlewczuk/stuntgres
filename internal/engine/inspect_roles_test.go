package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/session"
)

// ForeignAuthorizationOf calls foreignAuthorizationOf.
func ForeignAuthorizationOf(a dml.Authority) *AuthorizationView {
	return foreignAuthorizationOf(a)
}

// AuthorizationInputs returns x.inputs.
func AuthorizationInputs(x *AuthorizationView) *AuthorityInputs { return x.inputs }

// AuthorizationLive calls x.live.
func AuthorizationLive(x AuthorizationView) bool {
	return x.live()
}

// DatabaseClusterNotificationHead returns x.notificationHead.
func DatabaseClusterNotificationHead(x *DatabaseCluster) *session.NotificationQueuePosition {
	return x.notificationHead
}

// EngineRemapPreparedCatalog calls x.remapPreparedCatalog.
func EngineRemapPreparedCatalog(x *Engine, state *catalog.State, source authorization.Read, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) (*catalog.State, catalog.ProcedureRemapper, error) {
	return x.remapPreparedCatalog(state, source, targetSnapshot, expected)
}

// EngineRetainedRoutineProcedureRemapper calls x.retainedRoutineProcedureRemapper.
func EngineRetainedRoutineProcedureRemapper(x *Engine, current catalog.Read) catalog.ProcedureRemapper {
	return x.retainedRoutineProcedureRemapper(current)
}
