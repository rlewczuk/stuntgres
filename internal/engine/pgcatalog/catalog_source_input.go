package pgcatalog

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqltype"
	"time"
)

// A producer owns only its rows. The caller chooses coherent immutable roots
// and owns runtime capture policy, locking and query-source construction.
type Input struct {
	Context      context.Context
	Catalog      catalog.Read
	Roles        authorization.Read
	Owner        *catalog.Owner
	Ref          ids.ObjectRef
	Source       *catalog.Relation
	Runtime      Runtime
	RoleSettings RoleSettingsProjection
	// TypeNamePath is the immutable effective lookup path paired with this
	// statement capture. Domains requires it for format_type-compatible names.
	TypeNamePath []ids.ObjectAddress
	// Keep, when set, tests one key column's cell of a candidate row; the
	// producer omits the rows it rejects (see KeyColumns). Rows it rejects
	// are ones the statement's filter rejects.
	Keep Keep
}

// Keep is a producer row test of one key column. The zero Keep keeps every row.
type Keep struct {
	Column int
	Test   func(sqltype.Value) bool
}

// Rejects reports whether the test reads column and rejects the cell that
// cell builds. cell runs only when the test reads column.
func (k Keep) Rejects(column int, cell func() (sqltype.Value, error)) bool {
	if k.Test == nil || k.Column != column {
		return false
	}
	v, err := cell()
	return err == nil && !k.Test(v)
}

// Key columns whose producers skip rows by Input.Keep.
const (
	AttributeRelationColumn = 0 // pg_attribute.attrelid
	ClassOIDColumn          = 0 // pg_class.oid
	ClassNameColumn         = 1 // pg_class.relname
	ProcedureOIDColumn      = 0 // pg_proc.oid

	InfoRoutinesSpecificSchemaColumn   = 1 // information_schema.routines.specific_schema
	InfoRoutinesSpecificNameColumn     = 2 // information_schema.routines.specific_name
	InfoRoutinesSchemaColumn           = 4 // information_schema.routines.routine_schema
	InfoParametersSpecificSchemaColumn = 1 // information_schema.parameters.specific_schema
	InfoParametersSpecificNameColumn   = 2 // information_schema.parameters.specific_name
)

// KeyColumns lists the key columns of a system relation whose producer skips
// rows by Input.Keep, in the order a capture tries them.
func KeyColumns(relation ids.OID) []int {
	switch relation {
	case SettingsOID:
		return []int{SettingsNameColumn}
	case AttributeOID:
		return []int{AttributeRelationColumn}
	case ClassOID:
		return []int{ClassOIDColumn, ClassNameColumn}
	case ids.ProcedureClassID:
		return []int{ProcedureOIDColumn}
	case InfoRoutinesOID:
		return []int{InfoRoutinesSpecificSchemaColumn, InfoRoutinesSpecificNameColumn, InfoRoutinesSchemaColumn}
	case InfoParametersOID:
		return []int{InfoParametersSpecificSchemaColumn, InfoParametersSpecificNameColumn}
	}
	return nil
}

// Producer builds the rows of one system relation from a captured Input.
type Producer func(Input) ([][]sqltype.Value, error)

// Facts are value snapshots that Runtime visitors hand to producers.
type CursorFact struct {
	Name, Statement string
	Hold, Scroll    bool
	Created         sqltype.Value
}

// PreparedStatementFact borrows immutable definition signatures for this visit
// only. It carries no executable graph or session authority.
type PreparedStatementFact struct {
	Name, Statement string
	Created         time.Time
	FromSQL         bool
	ParameterCount  int
	ParameterTypes  []sqltype.Usage
	ParameterOIDs   []uint32
	Results         []catalog.Column
	HasResults      bool
}
type SettingsFact struct {
	Definition    settings.Definition
	Value, Reset  settings.Value
	Source        string
	ResetCaptured bool // Persisted/provider capture overrides nullable native reset.
}
type PreparedFact struct {
	XID          uint32
	GID          string
	Prepared     time.Time
	Owner        string
	OwnerPresent bool
	Database     string
}
type ForeignKeyComparison struct {
	Child, Parent int
	Comparison    sqltype.IndexForeignKeyComparison
}

// TriggerFact contains presentation values, never executable trigger recipes.
type TriggerFact struct {
	OID                      ids.OID
	Name                     string
	Insert, Update, Delete   bool
	Timing, Orientation      string
	OldTable, NewTable       string
	HasOldTable, HasNewTable bool
	Condition, Statement     string
	HasCondition             bool
}

// TriggerCatalogFact is a definition snapshot, borrowed only during the visit.
// Qual retains its own checked immutable stored definition.
type TriggerCatalogFact struct {
	OID, Relation, Function, Parent ids.OID
	Name                            string
	Mode                            catalog.TriggerMode
	Type                            int16
	Attributes                      []int32
	Arguments                       []string
	Qual                            sqltype.Value
}

// RewriteCatalogFact borrows identity facts and retains immutable definition trees.
type RewriteCatalogFact struct {
	OID          ids.OID
	Name, Event  string
	Mode         catalog.RuleMode
	Instead      bool
	Qual, Action sqltype.Value
}

// Capabilities take values, not query bindings or live authority objects.
// Constructing the adapter does not perform any of these operations.
type Runtime interface {
	CursorCount() int
	VisitCursors(func(CursorFact) error) error
	PreparedStatementCount() int
	VisitPreparedStatements(context.Context, func(PreparedStatementFact) error) error
	SessionSettings(context.Context) ([]SettingsFact, error)
	PreparedCount() int
	VisitPreparedTransactions(context.Context, func(PreparedFact) error) error
	Maintenance(ids.ObjectAddress) (MaintenanceFact, bool)
	VisitExtendedStatistics(context.Context, func(ids.OID, bool, sqltype.Value, sqltype.Value, sqltype.Value, sqltype.Value) error) error
	VisitAttributeStatistics(context.Context, *catalog.Relation, func([]sqltype.Value) error) error
	DatabaseFrozenXID() uint64
	VisitVacuumProgress(context.Context, func(VacuumProgressFact) error) error
	VisitCopyProgress(context.Context, func(CopyProgressFact) error) error
	VisitCreateIndexProgress(context.Context, func(CreateIndexProgressFact) error) error
	TimezoneInput() sqltype.InputEnvironment
	DatabaseName() string
	TemporaryOwner() ids.SessionToken
	CurrentPrincipal() authorization.PrincipalRef
	ValidateCapture(catalog.Read, authorization.Read, ids.ObjectRef, *catalog.Relation, ids.OID) error
	Effective(ids.ObjectRef, bool) (authorization.Privileges, error)
	CanUse(ids.ObjectRef) error
	SelectColumns(ids.ObjectRef, *catalog.Relation) error
	ValidateParameter(catalog.Read, *catalog.Parameter) error
	ForeignServerVisible(*catalog.ForeignObject, authorization.PrincipalRef) (bool, error)
	VisitCheckAttributes(context.Context, *catalog.Relation, expr.Expr, func(int32) error) error
	ExpressionReferences(context.Context, catalog.Read, expr.Expr) ([]ids.ObjectAddress, error)
	ViewReferences(context.Context, catalog.Read, *catalog.Relation) ([]ids.ObjectAddress, error)
	IndexSimpleAttribute(ids.ObjectAddress, catalog.IndexKey) (int32, bool)
	IndexReferences(context.Context, catalog.Read, *catalog.IndexDefinition) ([]ids.ObjectAddress, error)
	PartitionReferences(context.Context, catalog.Read, *catalog.Relation) ([]ids.ObjectAddress, error)
	ViewDefinition(context.Context, catalog.Read, *catalog.Relation) (string, error)
	ColumnExpression(context.Context, catalog.Read, *catalog.Relation, expr.Expr) (string, error)
	RoutineDefault(context.Context, catalog.Read, *catalog.Procedure, int) (string, error)
	RoutineTrees(context.Context, catalog.Read, *catalog.Procedure, *sqltype.Descriptor) (sqltype.Value, sqltype.Value, error)
	DomainExpression(context.Context, catalog.Read, *catalog.Type, expr.Expr) (string, error)
	ColumnUpdatable(context.Context, catalog.Read, *catalog.Relation, int32) (bool, error)
	ViewCapabilities(context.Context, catalog.Read, *catalog.Relation) (ViewCapability, error)
	IndexDefinition(context.Context, catalog.Read, *catalog.Relation) (string, error)
	IndexMetadata(context.Context, catalog.Read, *catalog.Relation, [6]*sqltype.Descriptor) (IndexMetadataFact, error)
	PartitionMetadata(context.Context, catalog.Read, *catalog.Relation, [4]*sqltype.Descriptor) (PartitionMetadataFact, error)
	VisitRuleDefinitions(context.Context, catalog.Read, *catalog.Relation, func(catalog.RuleFact, string) error) error
	VisitRewriteCatalog(context.Context, catalog.Read, *catalog.Relation, *sqltype.Descriptor, func(RewriteCatalogFact) error) error
	VisitTriggerDefinitions(context.Context, catalog.Read, *catalog.Relation, bool, func(TriggerFact) error) error
	VisitTriggerCatalog(context.Context, catalog.Read, *catalog.Relation, *sqltype.Descriptor, func(TriggerCatalogFact) error) error
	IndexKey(*catalog.Relation, catalog.IndexKey) (int32, expr.Expr, error)
	VisitSequenceDefinitions(context.Context, func(catalog.Descriptor) error) error
	ExpressionTree(*sqltype.Descriptor, expr.Expr) (sqltype.Value, error)
	PolicyTree(context.Context, catalog.Read, *catalog.Relation, *sqltype.Descriptor, catalog.PolicyPayload) (sqltype.Value, error)
	PolicyDisplay(context.Context, catalog.Read, *catalog.Relation, catalog.PolicyPayload) (string, error)
	VisitPublicationTables(context.Context, catalog.Read, *catalog.Publication, func(PublicationTableFact) error) error
	ForeignKeyComparisons(view catalog.Read, child, parent, index *catalog.Relation, key catalog.ForeignKeyConstraint) ([]ForeignKeyComparison, error)
	ValidateType(catalog.Read, *catalog.Type) error
	TypeOutput(*catalog.Owner, catalog.Read, authorization.Read) sqltype.OutputEnvironment
	ObjectACL(catalog.Read, ids.ObjectAddress) (authorization.ACLValue, error)
	ValidateACL(authorization.Read, catalog.Descriptor) error
	Pinned(catalog.Read, ids.ObjectAddress) bool
	OperatorSeeded(*catalog.Operator) bool
	OperatorDependencies(catalog.Read, *catalog.Operator) []ids.ObjectAddress
	InstalledProcedure(*catalog.Procedure) bool
}
