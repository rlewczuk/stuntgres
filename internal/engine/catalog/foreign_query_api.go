package catalog

import (
	"context"
	"database/sql/driver"
)

// ForeignQuerySlot describes an execution frame cell, not a table attribute.
// IDs are dense, zero-based and private to this request. Presence cells and
// ordering cache cells never become public result columns.
type ForeignQuerySlot struct {
	ID               int
	TypeName         string
	TypeOID          uint32
	TypeModifier     int32
	Nullable         bool
	NullabilityKnown bool
	OriginTable      uint32
	OriginAttribute  int16
	CollationOID     uint32
	Presence         int
	Unused           bool // Typed NULL padding, never an extra source-column read.
}

// ForeignQueryExpression is a closed typed expression: slot, literal,
// parameter, comparison, and/or/not, isnull/isnotnull, cast, collate, or present.
// Slot addresses the concatenation of child output frames. Parameter is one-based.
// present normalizes an outer-join sentinel to a nonnull Boolean.
// collate preserves its argument's value while applying the declared Type
// collation to comparison/grouping/ordering semantics; only negotiated builtin
// C semantics are currently emitted. Local collation OIDs alone do not prove
// equivalent provider semantics.
type ForeignQueryExpression struct {
	Kind                string
	Slot, Parameter     int
	Literal             driver.Value
	Type                ForeignQuerySlot
	Namespace, Operator string
	RoutineName         string
	RoutineOID          uint32
	Position            int
	Arguments           []ForeignQueryExpression
}

type ForeignQueryOrder struct {
	Expression             ForeignQueryExpression
	OutputSlot             int // Corresponding key cell in this stage's complete output.
	Descending, NullsFirst bool
}

type ForeignQueryAggregate struct {
	Namespace, Name string
	RoutineOID      uint32
	Arguments       []ForeignQueryExpression
}

// ForeignQueryStage is an immutable relational tree. Kind is source, join,
// filter, aggregate, order, limit, or evaluation. Outputs declares the complete
// frame, including hidden cache/presence cells. Aggregate Outputs address a
// result frame of keys then calls, with literal hidden grouping-set occurrence
// cells. Source outputs address declared source columns; its presence outputs
// are literal true sentinels established before any outer join.
type ForeignQueryStage struct {
	Kind, Join string
	Occurrence int
	Children   []ForeignQueryStage
	Outputs    []ForeignQueryExpression
	Slots      []ForeignQuerySlot
	Predicate  *ForeignQueryExpression
	Keys       []ForeignQueryExpression
	Aggregates []ForeignQueryAggregate
	Order      []ForeignQueryOrder
	Offset     int64
	Count      *int64
}

type ForeignQuerySource struct {
	Occurrence int
	Request    ForeignRequest
}

type ForeignQueryRequest struct {
	Stage       ForeignQueryStage
	Sources     []ForeignQuerySource
	Parameters  ForeignScanParameters
	Transaction ForeignTransaction
}

// Compatibility is a trusted provider's per-request correctness promise, not
// permission to override the engine's structural and authority barriers.
// StatementVisibility includes a common source snapshot and read-your-writes
// when participating in a transaction. Absent promises decline the path.
type ForeignQueryCompatibility struct {
	Expressions, Collations, StatementVisibility bool
}

type ForeignQueryPlan struct {
	Slots   []ForeignQuerySlot
	Private any // Immutable; live handles belong only to the returned scan.
}

type ForeignQueryPath struct {
	StartupCost, TotalCost float64
	Compatibility          ForeignQueryCompatibility
	Plan                   ForeignQueryPlan
}

type ForeignJoinPathProvider interface {
	GetForeignJoinPaths(context.Context, ForeignQueryRequest) ([]ForeignQueryPath, error)
}

type ForeignUpperPathProvider interface {
	GetForeignUpperPaths(context.Context, ForeignQueryRequest) ([]ForeignQueryPath, error)
}

type ForeignQueryScanProvider interface {
	BeginForeignQueryScan(context.Context, ForeignQueryRequest, ForeignQueryPlan) (ForeignScan, error)
}
