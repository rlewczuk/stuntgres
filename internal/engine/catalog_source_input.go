package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"time"
)

// A producer owns only its rows. The caller chooses coherent immutable roots
// and owns runtime capture policy, locking and query-source construction.
type catalogSourceInput struct {
	Context context.Context
	Catalog catalog.Read
	Roles   authorization.Read
	Owner   *catalog.Owner
	Ref     ids.ObjectRef
	Source  *catalog.Relation
	Runtime catalogSourceRuntime
}

type catalogSourceProducer func(catalogSourceInput) ([][]sqltype.Value, error)

type catalogCursorFact struct {
	Name, Statement string
	Hold, Scroll    bool
	Created         sqltype.Value
}
type catalogSettingsFact struct{ Value, Reset, Source string }
type catalogPreparedFact struct {
	XID          uint32
	GID          string
	Prepared     time.Time
	Owner        string
	OwnerPresent bool
	Database     string
}
type catalogForeignKeyComparison struct {
	Child, Parent int
	Comparison    sqltype.IndexForeignKeyComparison
}
type catalogRuleFact struct {
	OID         ids.OID
	Name, Event string
	Instead     bool
	References  []ids.ObjectAddress
}

// Capabilities take values, not query bindings or live authority objects.
// Constructing the adapter does not perform any of these operations.
type catalogSourceRuntime interface {
	CursorCount() int
	VisitCursors(func(catalogCursorFact) error) error
	SessionSettings() catalogSettingsFact
	PreparedCount() int
	VisitPreparedTransactions(context.Context, func(catalogPreparedFact) error) error
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
	VisitPolicyExpressions(catalog.PolicyDefinition, func(expr.Expr) error) error
	VisitCheckAttributes(context.Context, *catalog.Relation, expr.Expr, func(int32) error) error
	ExpressionReferences(context.Context, catalog.Read, expr.Expr) ([]ids.ObjectAddress, error)
	ViewReferences(context.Context, catalog.Read, *catalog.Relation) ([]ids.ObjectAddress, error)
	IndexSimpleAttribute(ids.ObjectAddress, catalog.IndexKey) (int32, bool)
	IndexReferences(context.Context, catalog.Read, *catalog.IndexDefinition) ([]ids.ObjectAddress, error)
	PartitionReferences(context.Context, catalog.Read, *catalog.Relation) ([]ids.ObjectAddress, error)
	HasRules(*catalog.Relation) bool
	VisitRules(*catalog.Relation, func(catalogRuleFact) error) error
	ViewOptions(*catalog.Relation) []string
	ViewDefinition(context.Context, catalog.Read, *catalog.Relation) (string, error)
	IndexDefinition(context.Context, catalog.Read, *catalog.Relation) (string, error)
	IndexKey(*catalog.Relation, catalog.IndexKey) (int32, expr.Expr, error)
	VisitSequenceDefinitions(context.Context, func(catalog.Descriptor) error) error
	ExpressionTree(*sqltype.Descriptor, expr.Expr) (sqltype.Value, error)
	ForeignKeyComparisons(view catalog.Read, child, parent, index *catalog.Relation, key catalog.ForeignKeyConstraint) ([]catalogForeignKeyComparison, error)
	ValidateType(catalog.Read, *catalog.Type) error
	TypeOutput(*catalog.Owner, catalog.Read, authorization.Read) sqltype.OutputEnvironment
	ObjectACL(catalog.Read, ids.ObjectAddress) (authorization.ACLValue, error)
	ValidateACL(authorization.Read, catalog.Descriptor) error
	Pinned(catalog.Read, ids.ObjectAddress) bool
	OperatorSeeded(*catalog.Operator) bool
	OperatorDependencies(catalog.Read, *catalog.Operator) []ids.ObjectAddress
	TypeProviderProcedure(*catalog.Procedure) bool
}
