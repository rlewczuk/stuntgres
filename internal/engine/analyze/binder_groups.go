package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The Binder groups below are held by value, not embedded, so each
// use names the group it touches. Copying a binder copies them exactly as it
// copied the flat fields they replace, and a zero group is the state of every
// binder that does not set it. The binder also holds the binding groups
// ViewCapture and PreparedCaptures (binding_groups.go) under the same
// names as Binding, so Describe copies them whole.

// targetContext describes the relation a statement writes and the names its
// expressions may use for it. DML, rule and generated-column binding set it;
// column resolution, RETURNING and target-use tracking read it.
type targetContext struct {
	Relation              *catalog.Relation
	InsertTarget          *catalog.Relation // Diagnostic candidate only, never a source.
	Ref                   ids.ObjectRef
	RuleNamespace         *RuleNamespace
	GeneratedOwner        *catalog.Relation
	deleteTarget          bool
	deleteUsingSources    bool
	mergeWhen             bool
	mergeInput            bool
	Name                  string
	alias                 string
	returningAliases      *returningAliases
	returningReadRequired bool
	Uses                  []int // Target column slots read by bound expressions.
}

// parameterState holds parameter inference: the inferred usages by number,
// the declared formals, and the statement's parameter rules.
type parameterState struct {
	Parameters            map[int]sqltype.Usage
	formalParameters      []sqltype.Usage
	ClosedRoutineFormals  bool                 // SQL body slots are declarations, not inferred public parameters.
	routineInputCollation sqltype.CollationRef // Quoted aggregate support only; implicit formal authority.
	RoutineName           string
	RoutineNames          []string
	ExecuteParameters     bool
	UpdateParameters      bool
	updateInputParameters map[int]sqltype.Usage
	AnyParameters         []plan.ParameterExpr
	NoParameters          bool
}

// ConsumerContext names the definition an expression is bound for (default,
// check, policy, index, partition, materialized view) and records the
// snapshot demands found while binding it.
type ConsumerContext struct {
	DefaultConsumer  bool
	RoutineDefault   bool
	DefaultTemporary ids.SessionToken
	// Selected timezone/calendar operand analysis owns its released rejection
	// diagnostic without admitting additional explicit cast providers.
	DatetimeZoneOperands    bool
	CheckConsumer           bool
	PolicyConsumer          bool
	IndexConsumer           bool
	IndexPredicateConsumer  bool
	PartitionConsumer       string
	CheckDefinitionEquality bool
	VisibilitySnapshot      bool
	CatalogSnapshot         bool // Demanded inquiry nodes after STRICT folding.
	// Materialized definitions need frozen renderer metadata while retaining
	// their distinct, post-analysis bound-parameter diagnostic.
	frozenDefinition bool // Shared immutable presentation capture (view, materialized view, routine).
}

// StoredSyntaxState keeps stored SQL stable across reparsing: the literal
// nodes of an internal TYPE CHECK reparse, the frozen syntax of analyzed
// lowerings, and the occurrence counters of VALUES lists, joins and ROWS FROM
// function lists that key their view literal paths.
type StoredSyntaxState struct {
	// Only internal TYPE CHECK reparsing supplies these immutable literal nodes.
	// Generated markers are not prepared parameters and never enter inference.
	CheckLiterals []plan.LiteralExpr
	// Frozen syntax retains analyzed lowering without changing prepared source.
	// Analysis-only templates never enter executable nodes or prepared frames.
	Syntax        map[StoredExpressionSyntaxKey]parser.Expr
	valuesCount   int
	joinCount     int
	functionCount int
}

// ClauseState is the state of the clause being bound: its name for
// diagnostics, the string and regexp set-returning guards, and the
// selected-call context of an operator or SQL routine occurrence. Clause
// binders save it, set it and restore it around their children.
type ClauseState struct {
	AggregateClause     string
	StringSetGuard      string
	stringFromForbidden bool
	stringSingleInsert  bool
	QueryHasTargetSet   bool
	FromSetObservation  FromSetObservation
	RegexpClause        string
	JSONBRecordBase     bool // Scope named cast identity to selected base trees.
	DomainValueUsage    *sqltype.Usage
	FunctionProjection  FunctionProjectionScope
	// Selected operator and SQL routine calls bind their arguments once and
	// expose them to provider binding through these slots.
	SelectedOperatorArgs   []expr.Expr
	SelectedOperatorSyntax []parser.Expr
	SelectedCallArguments  bool              // Known arguments remain reusable throughout provider binding.
	selectedCallReentry    bool              // Only the immediate selected occurrence skips selection once.
	selectedWindowCallee   parser.ObjectName // Diagnostic spelling, never lookup authority.
	selectedAggregate      *catalog.Procedure
}

// GroupingState collects the aggregates, grouping sets and window calls of
// the query level being bound, with the window definitions they resolve
// against.
type GroupingState struct {
	Aggregates        []plan.AggregateCall
	active            bool
	GroupSets         [][]int
	groupSubquery     bool
	windowRaw         []parser.WindowDefinition
	windowDefinitions map[string]plan.WindowSpec
	WindowCalls       []plan.WindowCall
	ArgumentOwner     ArgumentWindowOwner
}

// QueryScopeState is the query level being bound: its authorization view,
// sources and lock admission, the visible namespace and hidden qualifiers,
// frame numbering, and the scope stack of enclosing levels. Subquery and
// FROM binders save it and restore it around nested levels.
type QueryScopeState struct {
	Auth                  Authority
	Sources               []exec.Leaf
	lockAdmission         *LockAdmission
	Namespace             *QueryNamespace
	Hidden                []QueryQualifier
	NonLateral            bool
	Frame, NextQueryFrame uint32
	resolvedQueryFrame    uint32
	Scopes                []queryBindingScope
	presenceFrames        map[uint32]int
	suppressWholePresence bool
	rowDiagnosticMode     lock.Mode // Clause diagnostics only; tuple authority is plan.RowMark.
	subqueryDemand        bool
}

// cteState holds the WITH definitions visible to the statement, the
// recursive definition being bound, and the CTE executables built so far.
type cteState struct {
	bindings            []cteBinding
	recursiveDefinition *plan.CTEDefinition
	recursiveFrames     map[uint32]bool
	scanOrdinal         int
	analysisDepth       int
	executables         []*plan.Query
	statementCTEs       []*plan.CTEDefinition
	selectBodies        bool
}
