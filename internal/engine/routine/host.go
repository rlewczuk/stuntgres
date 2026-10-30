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
// methods below. Methods enter and return with Engine.Mu held.
type DDLHost interface {
	ddl.Host

	DefinitionType(ddl.Authority, sqltype.ID) (*sqltype.Descriptor, bool)
	DefinitionLookup(ddl.Authority, ids.NameKey) ids.ObjectAddress
	// SelectFunction selects the routines with exactly signature on the
	// function path, as regprocedure input does.
	SelectFunction(auth ddl.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (catalog.RoutineIdentity, int, error)
	// RoutineSignatureSpelling renders canonical regprocedure output using the
	// authority's reference path, including signature masking and type visibility.
	RoutineSignatureSpelling(ddl.Authority, ids.OID) (string, error)
	// ResolveRegtype resolves a type name as regtype input does.
	ResolveRegtype(ddl.Authority, parser.TypeName) (ids.OID, error)
	SelectAggregateSupport(ddl.Authority, parser.ObjectName, []sqltype.Usage) (*catalog.Procedure, []sqltype.Usage, sqltype.Usage, error)
	ValidateAggregateInitial(ddl.Authority, sqltype.Usage, string, sqltype.InputEnvironment) error
	// ValidateRoutineSetting validates/captures a declaration without installing
	// it. It may suspend the ordered command, release all publication locks for
	// Normalize, then reacquire Engine.Mu and readmit its exact live authority.
	ValidateRoutineSetting(auth ddl.Authority, clause *parser.Set) (name string, value string, remove bool, err error)
	// ValidateRoutineConfiguration preflights validator SAVE actions using the
	// entry setting, then returns the derived body policy without installing
	// configuration. ALTER does not invoke a language validator.
	ValidateRoutineConfiguration(d *catalog.Procedure) (validateBody bool, err error)
	// BindSQLRoutine always analyzes/freezes standard bodies; validateBody
	// controls language-validator body checks, independently of private replay.
	BindSQLRoutine(auth ddl.Authority, d *catalog.Procedure, input sqltype.InputEnvironment, validateBody bool) error
	BindSQLDefaults(ddl.Authority, *catalog.Procedure, []parser.RoutineParameter, sqltype.InputEnvironment) error
	MaterializeHostConversions() error
	RefreshTransformConsumers(catalog.Read,*catalog.Transform) ([]catalog.Descriptor,error)
}
