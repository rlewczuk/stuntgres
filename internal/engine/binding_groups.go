package engine

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

// insertBinding is the INSERT payload. INSERT binding, MERGE INSERT arms and
// view-write INSERT translation write it; execution, rule rewriting and
// ON CONFLICT admission read it.
type insertBinding struct {
	SubquerySource  bool // Analyzed SELECT or VALUES with query clauses; normalization never changes source ownership.
	Rows            [][]parser.Expr
	Defaults        []expr.Expr
	DefaultOrigins  []defaultOrigin
	IdentityDiscard []bool
	Projection      []int
	Conflict        insertConflictSlot
}

// deleteBinding is the DELETE payload: the USING source and its frame width.
// DELETE binding and rule grafting write it; execution and rule rewriting
// read it.
type deleteBinding struct {
	Using       plan.Node
	SourceWidth int
}

// createTableBinding is the CREATE TABLE payload. describe writes it, with
// inheritance merged by describeInheritance; CREATE TABLE execution reads it.
type createTableBinding struct {
	Columns      []catalog.Column
	NotNull      []catalog.NotNullConstraint
	ColumnIndex  map[string]int
	Uniques      []createUniqueConstraint
	Declarations []createColumnDeclaration
	Likes        []createLikeInput
	OfType       *catalog.Type
	Inheritance  []createInheritanceInput
	LocalColumns map[string]bool
}

// viewCapture holds the expression binder's rendering captures, keyed by
// literal path. View, rule and policy definitions read them to deparse stored
// SQL; the maps keep the binder's contents and iteration rules.
type viewCapture struct {
	Literals          map[viewLiteralPath]plan.LiteralExpr
	CastUsages        map[viewLiteralPath]sqltype.Usage
	RoutineRefs       map[viewLiteralPath]ids.ObjectRef
	OperatorRefs      map[viewLiteralPath]ids.ObjectRef
	Composites        map[viewLiteralPath]compositeOccurrence
	SimilarSubstrings map[viewLiteralPath]bool
	PosixSubstrings   map[viewLiteralPath]bool
	Collations        map[viewLiteralPath]uint32
	Columns           map[viewLiteralPath]viewColumn
	Stars             map[viewLiteralPath][]viewColumn
	Namespaces        []viewNamespace
}

// preparedCaptures holds the constants a prepared statement re-resolves
// against the current environment before each execution.
type preparedCaptures struct {
	Temporals         []temporalConstant
	Composites        map[preparedCompositeKey]compositeOccurrence
	SequenceConstants []sequence.Constant
	RegclassConcat    bool
	RegtypeConstants  []regtypeConstant
}

// resolverInputs holds the statement input resolvers captured from the
// binder's input environment; execution restores them into its own.
type resolverInputs struct {
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

// newResolverInputs captures every statement input resolver of input.
func NewResolverInputs(input sqltype.InputEnvironment) resolverInputs {
	return resolverInputs{
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

// ruleBinding marks a rule action template and maps its CTEs to their rule
// commands.
type ruleBinding struct {
	Template bool
	CTEs     ruleCTEsSlot
}

// The slots below carry engine-owned values through a binding without
// naming their types. The engine stores and recovers each value with one
// checked helper (binding_slots.go) and never stores a nil pointer, so a nil
// slot means the value is absent.

// insertConflictSlot holds the bound ON CONFLICT clause together with its
// per-execution arbiter and write state.
type insertConflictSlot interface{ InsertConflictSlot() }

// ruleCTEsSlot holds the rule commands of a rule action's CTEs, keyed by
// their definitions.
type ruleCTEsSlot interface{ RuleCTEsSlot() }

// viewWriteSlot holds the lowering of a write through an updatable view.
type viewWriteSlot interface{ ViewWriteSlot() }
