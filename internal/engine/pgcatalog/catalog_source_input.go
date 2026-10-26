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
	Context context.Context
	Catalog catalog.Read
	Roles   authorization.Read
	Owner   *catalog.Owner
	Ref     ids.ObjectRef
	Source  *catalog.Relation
	Runtime Runtime
	// TypeNamePath is the immutable effective lookup path paired with this
	// statement capture. Domains requires it for format_type-compatible names.
	TypeNamePath []ids.ObjectAddress
}

// Producer builds the rows of one system relation from a captured Input.
type Producer func(Input) ([][]sqltype.Value, error)

// Facts are value snapshots that Runtime visitors hand to producers.
type CursorFact struct {
	Name, Statement string
	Hold, Scroll    bool
	Created         sqltype.Value
}
type SettingsFact struct {
	Definition   settings.Definition
	Value, Reset settings.Value
	Source       string
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

// Capabilities take values, not query bindings or live authority objects.
// Constructing the adapter does not perform any of these operations.
type Runtime interface {
	CursorCount() int
	VisitCursors(func(CursorFact) error) error
	SessionSettings(context.Context) ([]SettingsFact, error)
	PreparedCount() int
	VisitPreparedTransactions(context.Context, func(PreparedFact) error) error
	Maintenance(ids.ObjectAddress) (MaintenanceFact, bool)
	DatabaseFrozenXID() uint64
	VisitVacuumProgress(context.Context, func(VacuumProgressFact) error) error
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
	ColumnUpdatable(context.Context, catalog.Read, *catalog.Relation, int32) (bool, error)
	ViewCapabilities(context.Context, catalog.Read, *catalog.Relation) (ViewCapability, error)
	IndexDefinition(context.Context, catalog.Read, *catalog.Relation) (string, error)
	IndexVectors(context.Context, catalog.Read, *catalog.Relation, [4]*sqltype.Descriptor) ([4]sqltype.Value, error)
	VisitRuleDefinitions(context.Context, catalog.Read, *catalog.Relation, func(catalog.RuleFact, string) error) error
	VisitTriggerDefinitions(context.Context, catalog.Read, *catalog.Relation, bool, func(TriggerFact) error) error
	IndexKey(*catalog.Relation, catalog.IndexKey) (int32, expr.Expr, error)
	VisitSequenceDefinitions(context.Context, func(catalog.Descriptor) error) error
	ExpressionTree(*sqltype.Descriptor, expr.Expr) (sqltype.Value, error)
	ForeignKeyComparisons(view catalog.Read, child, parent, index *catalog.Relation, key catalog.ForeignKeyConstraint) ([]ForeignKeyComparison, error)
	ValidateType(catalog.Read, *catalog.Type) error
	TypeOutput(*catalog.Owner, catalog.Read, authorization.Read) sqltype.OutputEnvironment
	ObjectACL(catalog.Read, ids.ObjectAddress) (authorization.ACLValue, error)
	ValidateACL(authorization.Read, catalog.Descriptor) error
	Pinned(catalog.Read, ids.ObjectAddress) bool
	OperatorSeeded(*catalog.Operator) bool
	OperatorDependencies(catalog.Read, *catalog.Operator) []ids.ObjectAddress
	TypeProviderProcedure(*catalog.Procedure) bool
}
