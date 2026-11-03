package extension

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
)

// Stage is the catalog stage a provider installs its members into. The
// engine implements it over its own stage, allocator and authority. Every
// method is called with the engine's catalog mutex held, so implementations
// must not call back into anything that takes it.
type Stage interface {
	ddl.Allocator
	ddl.TypeStaging

	// InitializePgcrypto performs the stock provider's physical-session C
	// initializer at its actual member-validation point, not at catalog lookup.
	InitializePgcrypto(scriptContext string) error
	// ActivatePgTrgm reaches the first stock setter's C validation only after
	// its declaration has passed the ordinary duplicate-name check.
	ActivatePgTrgm() error
	// Read is the catalog the stage was begun from (its base).
	// Roles is the installation authority's coherent role snapshot.
	Roles() authorization.Read
	Read() catalog.Read
	// CurrentRead includes both the immutable base and unpublished changes.
	CurrentRead() catalog.Read
	// Get reads only staged creations/replacements, as used by sealing.
	// Resolve incumbent dependencies through CurrentRead, not Get.
	Get(ids.ObjectAddress) catalog.Descriptor
	LookupName(ids.NameKey) ids.ObjectAddress
	Create(catalog.Descriptor) error
	Replace(catalog.Descriptor) error
	// ObserveNamespace fences every script target, explicit schema and actual
	// prerequisite namespace against its captured committed predecessor.
	ObserveNamespace(ids.ObjectAddress)
	// ObservePrerequisite retains the exact incumbent extension descriptor;
	// dependencies created by this stage already have CREATE observations.
	ObservePrerequisite(ids.ObjectAddress)
	// CreationRoutineACL observes the creator's ordinary routine defaults.
	CreationRoutineACL(authorization.PrincipalRef, ids.ObjectAddress) authorization.ACLValue
	// RoutineCreationACLWithoutPublic applies the supported script's PUBLIC
	// EXECUTE revoke to the creator's global/schema routine defaults.
	RoutineCreationACLWithoutPublic(authorization.PrincipalRef, ids.ObjectAddress) (authorization.ACLValue, error)
	// RecordInitialPrivilege records an actual script ACL event, not membership
	// or an unrelated descriptor replacement.
	RecordInitialPrivilege(ids.ObjectAddress, byte, authorization.ACLValue)
	// ScriptFunctions resolves helpers as owner through the actual extension's
	// target and prerequisite schemas, replacing the caller path and excluding
	// TEMP functions. It checks schema USAGE under that script principal.
	ScriptFunctions(owner authorization.PrincipalRef, ext *catalog.Extension, helpers []ScriptFunction) ([]*catalog.Procedure, error)
	// EarthChecks attaches the three genuine provider domain recipes using
	// the engine's ordinary constraint allocator/renderer/dependency authority.
	EarthChecks(*catalog.Type) error
	// Composite stages a standalone row type and its implicit array through
	// the ordinary relation allocator, row-type codecs and creation ACLs.
	Composite(ids.ObjectAddress, string, authorization.PrincipalRef, []catalog.Column, ids.ObjectAddress) (*catalog.Type, *catalog.Type, error)
}

// ScriptFunction names one helper function an extension script looks up.
type ScriptFunction struct {
	Name      string
	Signature catalog.ProcedureSignature
}

// Installer is an extension implementation that can stage its base version.
type Installer interface {
	catalog.ExtensionImplementation
	Stage(Stage, *catalog.Extension, authorization.PrincipalRef) error
}

// Updater is an implementation with executable version transitions.
type Updater interface {
	Updates() []UpdateAction
}

// UpdateAction stages one version edge of an installed extension.
type UpdateAction struct {
	Edge  catalog.ExtensionVersionEdge
	Stage func(Stage, *catalog.Extension, authorization.PrincipalRef) error
}
