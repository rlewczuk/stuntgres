package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// DDLHost is what the operator and CREATE/ALTER/DROP routine handlers need from the
// engine for one schema statement: the shared ddl.Host plus the routine
// methods below. Every method runs with Engine.mu held and never takes or
// releases it.
type DDLHost interface {
	ddl.Host

	DefinitionType(ddl.Authority, sqltype.ID) (*sqltype.Descriptor, bool)
	DefinitionLookup(ddl.Authority, ids.NameKey) ids.ObjectAddress
	// SelectFunction selects the routines with exactly signature on the
	// function path, as regprocedure input does.
	SelectFunction(auth ddl.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (catalog.RoutineIdentity, int, error)
	// ResolveRegtype resolves a type name as regtype input does.
	ResolveRegtype(ddl.Authority, parser.TypeName) (ids.OID, error)
	SelectAggregateSupport(ddl.Authority, parser.ObjectName, []sqltype.Usage) (*catalog.Procedure, []sqltype.Usage, sqltype.Usage, error)
	// BindSQLRoutine binds d's body against the stage. It returns nil during
	// Restore SQL body replay.
	BindSQLRoutine(auth ddl.Authority, d *catalog.Procedure, input sqltype.InputEnvironment) error
	BindSQLDefaults(ddl.Authority, *catalog.Procedure, []parser.RoutineParameter, sqltype.InputEnvironment) error
}
