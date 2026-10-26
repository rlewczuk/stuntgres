package core

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
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

// SettingAccess calls settingAccess.
func SettingAccess(d settings.Definition, op *session.Operation) error {
	return settingAccess(d, op)
}

// AuthorizationActingAs calls x.actingAs.
func AuthorizationActingAs(x AuthorizationView, owner authorization.PrincipalRef) AuthorizationView {
	return x.actingAs(owner)
}

// AuthorizationNamespaceUser returns x.namespaceUser.
func AuthorizationNamespaceUser(x *AuthorizationView) authorization.PrincipalRef {
	return x.namespaceUser
}

// AuthorizationPathPrefix returns x.pathPrefix.
func AuthorizationPathPrefix(x *AuthorizationView) ids.ObjectAddress { return x.PathPrefix }

// AuthorizationSettings returns x.settings.
func AuthorizationSettings(x *AuthorizationView) *session.SettingsStore { return x.settings }

// SetAuthorizationPathPrefix sets x.pathPrefix.
func SetAuthorizationPathPrefix(x *AuthorizationView, v ids.ObjectAddress) { x.PathPrefix = v }

// SetDatabaseClusterNotificationHead sets x.notificationHead.
func SetDatabaseClusterNotificationHead(x *DatabaseCluster, v *session.NotificationQueuePosition) {
	x.notificationHead = v
}

// EngineJournalRolePublicationLocked calls x.journalRolePublicationLocked.
func EngineJournalRolePublicationLocked(x *Engine, c *session.Journal, materialize bool) (*access.RoleRecord, error) {
	return x.journalRolePublicationLocked(c, materialize)
}

// EnginePublicationRolesLocked calls x.publicationRolesLocked.
func EnginePublicationRolesLocked(x *Engine, ctx context.Context, c *session.Journal) (*session.Journal, error) {
	return x.publicationRolesLocked(ctx, c)
}

// EngineRoleSessionCountLocked calls x.roleSessionCountLocked.
func EngineRoleSessionCountLocked(x *Engine, ref authorization.PrincipalRef) int {
	return x.roleSessionCountLocked(ref)
}

// AuthorizationCanReadRestrictedSettings calls x.canReadRestrictedSettings.
func AuthorizationCanReadRestrictedSettings(x AuthorizationView) (bool, error) {
	return x.canReadRestrictedSettings()
}
