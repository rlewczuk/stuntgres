package routine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Authority is the statement authority the DDL handlers carry. It is an
// opaque value: handlers keep it by value, and a host method that changes it
// returns a new one and leaves its argument alone, so an authority changed
// inside one helper (an implicit namespace, a peer recapture) stays local to
// that helper.
type Authority interface {
	access.Environment
	RoutineAuthority()
}

// Stage is the private catalog change a DDL statement writes. Create and
// Replace keep the engine stage's lifecycle checks.
type Stage interface {
	Create(catalog.Descriptor) error
	Replace(catalog.Descriptor) error
	UpdateDescriptor(catalog.Descriptor)
	Get(ids.ObjectAddress) catalog.Descriptor
	LookupName(ids.NameKey) ids.ObjectAddress
}

// DDLHost is what the operator and CREATE FUNCTION handlers need from the
// engine for one schema statement. Every method runs with Engine.mu held and
// never takes or releases it.
type DDLHost interface {
	// AccessView returns the catalog, roles, state and owner of auth.
	AccessView(Authority) access.View
	// Recapture returns auth reading view.
	Recapture(auth Authority, view catalog.Read) Authority
	DefinitionType(Authority, sqltype.ID) (*sqltype.Descriptor, bool)
	DefinitionLookup(Authority, ids.NameKey) ids.ObjectAddress

	// CreationNamespace resolves the namespace an object is created in. It may
	// stage implicit namespaces; the returned authority then reads the stage.
	CreationNamespace(Authority, parser.ObjectName) (ids.ObjectRef, Authority, error)
	// CheckCreateIn checks CREATE on a namespace, with the trusted Restore
	// exception.
	CheckCreateIn(Authority, ids.ObjectRef) error
	OperatorNamespaces(Authority, parser.ObjectName) ([]ids.ObjectAddress, error)
	// LookupFunction finds the function with exactly signature on the
	// function path; nil without error when there is none.
	LookupFunction(auth Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (*catalog.Procedure, error)
	// SelectFunction selects the routines with exactly signature on the
	// function path, as regprocedure input does.
	SelectFunction(auth Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (catalog.RoutineIdentity, int, error)
	ResolveType(Authority, parser.TypeName) (ids.OID, error)
	DependencyObject(Authority, ids.ObjectAddress) string

	Stage() Stage
	StageBase() catalog.Read
	StageRead() catalog.Read
	CreationACL(owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue
	DropTarget(ids.ObjectAddress)
	RecordDependencyPath(Authority)
	AllocateOID() (ids.OID, error)
	Revision() (ids.DefinitionRevision, error)

	EmitNotice(session.NoticeLevel, sqlstate.Error)
	// BindSQLRoutine binds d's body against the stage. It returns nil during
	// Restore SQL body replay.
	BindSQLRoutine(auth Authority, d *catalog.Procedure, input sqltype.InputEnvironment) error
}
