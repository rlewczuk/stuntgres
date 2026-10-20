package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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
	subquerySource  bool // Analyzed SELECT or VALUES with query clauses; normalization never changes source ownership.
	rows            [][]parser.Expr
	defaults        []expr.Expr
	defaultOrigins  []defaultOrigin
	identityDiscard []bool
	projection      []int
	conflict        *boundInsertConflict
}

// deleteBinding is the DELETE payload: the USING source and its frame width.
// DELETE binding and rule grafting write it; execution and rule rewriting
// read it.
type deleteBinding struct {
	using       LogicalNode
	sourceWidth int
}

// createTableBinding is the CREATE TABLE payload. describe writes it, with
// inheritance merged by describeInheritance; CREATE TABLE execution reads it.
type createTableBinding struct {
	columns      []catalog.Column
	notNull      []catalog.NotNullConstraint
	columnIndex  map[string]int
	uniques      []createUniqueConstraint
	declarations []createColumnDeclaration
	likes        []createLikeInput
	ofType       *catalog.Type
	inheritance  []createInheritanceInput
	localColumns map[string]bool
}

// viewCapture holds the expression binder's rendering captures, keyed by
// literal path. View, rule and policy definitions read them to deparse stored
// SQL; the maps keep the binder's contents and iteration rules.
type viewCapture struct {
	literals          map[viewLiteralPath]literalExpression
	castUsages        map[viewLiteralPath]sqltype.Usage
	routineRefs       map[viewLiteralPath]ids.ObjectRef
	operatorRefs      map[viewLiteralPath]ids.ObjectRef
	composites        map[viewLiteralPath]compositeOccurrence
	similarSubstrings map[viewLiteralPath]bool
	posixSubstrings   map[viewLiteralPath]bool
	collations        map[viewLiteralPath]uint32
	columns           map[viewLiteralPath]viewColumn
	stars             map[viewLiteralPath][]viewColumn
	namespaces        []viewNamespace
}

// preparedCaptures holds the constants a prepared statement re-resolves
// against the current environment before each execution.
type preparedCaptures struct {
	temporals         []temporalConstant
	composites        map[preparedCompositeKey]compositeOccurrence
	sequenceConstants []sequence.Constant
	regclassConcat    bool
	regtypeConstants  []regtypeConstant
}

// resolverInputs holds the statement input resolvers captured from the
// binder's input environment; execution restores them into its own.
type resolverInputs struct {
	regclass           sqltype.RegclassResolver
	regtype            sqltype.RegtypeResolver
	regrole            sqltype.RegroleResolver
	operator           sqltype.OperatorResolver
	regproc            sqltype.RoutineResolver
	regcollation       sqltype.RegcollationResolver
	regnamespace       sqltype.RegnamespaceResolver
	regTS              sqltype.RegTSResolver
	aclitem            sqltype.ACLItemResolver
	fts                sqltype.FTSInputNotices
	typeDiagnosticName func(sqltype.ID) string
	enumMemberSafe     func(sqltype.ID, int64) bool
}

// newResolverInputs captures every statement input resolver of input.
func newResolverInputs(input sqltype.InputEnvironment) resolverInputs {
	return resolverInputs{
		regclass:           input.Regclass,
		regtype:            input.Regtype,
		regrole:            input.Regrole,
		operator:           input.Operator,
		regproc:            input.Regproc,
		regcollation:       input.Regcollation,
		regnamespace:       input.Regnamespace,
		regTS:              input.RegTS,
		aclitem:            input.ACLItem,
		fts:                input.FTS,
		typeDiagnosticName: input.TypeDiagnosticName,
		enumMemberSafe:     input.EnumMemberSafe,
	}
}

// ruleBinding marks a rule action template and maps its CTEs to their rule
// commands.
type ruleBinding struct {
	template bool
	ctes     map[*cteDefinition]*ruleCTECommand
}
