package rolecmds

// Exported names of rolecmds internals for the external tests in this
// directory.

var (
	DefaultACLChangeLocked       = defaultACLChangeLocked
	NewDatabaseRuntime           = newDatabaseRuntime
	PublishDatabaseTargetLocked  = publishDatabaseTargetLocked
	PublishDroppedRolesLocked    = publishDroppedRolesLocked
	SwitchSessionRoleLocked      = switchSessionRoleLocked
	TouchOperationIdentityLocked = touchOperationIdentityLocked
)
