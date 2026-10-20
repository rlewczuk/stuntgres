package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The binding groups below are held by value, so copying a binding copies
// them exactly as it copied the flat fields they replace. A zero group is the
// state of every statement kind that does not write it.

// InsertBinding is the INSERT payload. INSERT binding, MERGE INSERT arms and
// view-write INSERT translation write it; execution, rule rewriting and
// ON CONFLICT admission read it.
type InsertBinding struct {
	SubquerySource  bool // Analyzed SELECT or VALUES with query clauses; normalization never changes source ownership.
	Rows            [][]parser.Expr
	Defaults        []expr.Expr
	DefaultOrigins  []DefaultOrigin
	IdentityDiscard []bool
	Projection      []int
	Conflict        InsertConflictSlot
}

// DeleteBinding is the DELETE payload: the USING source and its frame width.
// DELETE binding and rule grafting write it; execution and rule rewriting
// read it.
type DeleteBinding struct {
	Using       plan.Node
	SourceWidth int
}

// CreateTableBinding is the CREATE TABLE payload. describe writes it, with
// inheritance merged by describeInheritance; CREATE TABLE execution reads it.
type CreateTableBinding struct {
	Columns      []catalog.Column
	NotNull      []catalog.NotNullConstraint
	ColumnIndex  map[string]int
	Uniques      []UniqueConstraint
	Declarations []ColumnDeclaration
	Likes        []LikeInput
	OfType       *catalog.Type
	Inheritance  []InheritanceInput
	LocalColumns map[string]bool
}

// ViewCapture holds the expression binder's rendering captures, keyed by
// literal path. View, rule and policy definitions read them to deparse stored
// SQL; the maps keep the binder's contents and iteration rules.
type ViewCapture struct {
	Literals          map[ViewLiteralPath]plan.LiteralExpr
	CastUsages        map[ViewLiteralPath]sqltype.Usage
	RoutineRefs       map[ViewLiteralPath]ids.ObjectRef
	OperatorRefs      map[ViewLiteralPath]ids.ObjectRef
	Composites        map[ViewLiteralPath]CompositeOccurrence
	SimilarSubstrings map[ViewLiteralPath]bool
	PosixSubstrings   map[ViewLiteralPath]bool
	Collations        map[ViewLiteralPath]uint32
	Columns           map[ViewLiteralPath]ViewColumn
	Stars             map[ViewLiteralPath][]ViewColumn
	Namespaces        []ViewNamespace
}

// PreparedCaptures holds the constants a prepared statement re-resolves
// against the current environment before each execution.
type PreparedCaptures struct {
	Temporals         []TemporalConstant
	Composites        map[PreparedCompositeKey]CompositeOccurrence
	SequenceConstants []sequence.Constant
	RegclassConcat    bool
	RegtypeConstants  []RegtypeConstant
}

// ResolverInputs holds the statement input resolvers captured from the
// binder's input environment; execution restores them into its own.
type ResolverInputs struct {
	Regclass           sqltype.RegclassResolver
	Regtype            sqltype.RegtypeResolver
	Regrole            sqltype.RegroleResolver
	Operator           sqltype.OperatorResolver
	Regproc            sqltype.RoutineResolver
	Regcollation       sqltype.RegcollationResolver
	Regnamespace       sqltype.RegnamespaceResolver
	RegTS              sqltype.RegTSResolver
	ACLItem            sqltype.ACLItemResolver
	FTS                sqltype.FTSInputNotices
	TypeDiagnosticName func(sqltype.ID) string
	EnumMemberSafe     func(sqltype.ID, int64) bool
}

// NewResolverInputs captures every statement input resolver of input.
func NewResolverInputs(input sqltype.InputEnvironment) ResolverInputs {
	return ResolverInputs{
		Regclass:           input.Regclass,
		Regtype:            input.Regtype,
		Regrole:            input.Regrole,
		Operator:           input.Operator,
		Regproc:            input.Regproc,
		Regcollation:       input.Regcollation,
		Regnamespace:       input.Regnamespace,
		RegTS:              input.RegTS,
		ACLItem:            input.ACLItem,
		FTS:                input.FTS,
		TypeDiagnosticName: input.TypeDiagnosticName,
		EnumMemberSafe:     input.EnumMemberSafe,
	}
}

// RuleBinding marks a rule action template and maps its CTEs to their rule
// commands.
type RuleBinding struct {
	Template bool
	CTEs     RuleCTEsSlot
}

// The slots below carry engine-owned values through a binding without
// naming their types. The engine stores and recovers each value with one
// checked helper (the engine's binding_slots.go) and never stores a nil pointer, so a nil
// slot means the value is absent.

// InsertConflictSlot holds the bound ON CONFLICT clause together with its
// per-execution arbiter and write state.
type InsertConflictSlot interface{ InsertConflictSlot() }

// RuleCTEsSlot holds the rule commands of a rule action's CTEs, keyed by
// their definitions.
type RuleCTEsSlot interface{ RuleCTEsSlot() }

// ViewWriteSlot holds the lowering of a write through an updatable view.
type ViewWriteSlot interface{ ViewWriteSlot() }
