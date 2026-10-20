package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// The expressionBinder groups below are held by value, not embedded, so each
// use names the group it touches. Copying a binder copies them exactly as it
// copied the flat fields they replace, and a zero group is the state of every
// binder that does not set it. The binder also holds the binding groups
// viewCapture and preparedCaptures (binding_groups.go) under the same names
// as binding, so describe copies them whole.

// targetContext describes the relation a statement writes and the names its
// expressions may use for it. DML, rule and generated-column binding set it;
// column resolution, RETURNING and target-use tracking read it.
type targetContext struct {
	relation              *catalog.Relation
	insertTarget          *catalog.Relation // Diagnostic candidate only, never a source.
	ref                   ids.ObjectRef
	ruleNamespace         *ruleNamespace
	generatedOwner        *catalog.Relation
	deleteTarget          bool
	deleteUsingSources    bool
	mergeWhen             bool
	mergeInput            bool
	name                  string
	alias                 string
	returningAliases      *returningAliases
	returningReadRequired bool
	uses                  []int // Target column slots read by bound expressions.
}

// parameterState holds parameter inference: the inferred usages by number,
// the declared formals, and the statement's parameter rules.
type parameterState struct {
	parameters            map[int]sqltype.Usage
	formalParameters      []sqltype.Usage
	closedRoutineFormals  bool // SQL body slots are declarations, not inferred public parameters.
	executeParameters     bool
	updateParameters      bool
	updateInputParameters map[int]sqltype.Usage
	anyParameters         []parameterExpression
	noParameters          bool
}

// consumerContext names the definition an expression is bound for (default,
// check, policy, index, partition, materialized view) and records the
// snapshot demands found while binding it.
type consumerContext struct {
	defaultConsumer  bool
	defaultTemporary ids.SessionToken
	// Selected timezone/calendar operand analysis owns its released rejection
	// diagnostic without admitting additional explicit cast providers.
	datetimeZoneOperands    bool
	checkConsumer           bool
	policyConsumer          bool
	indexConsumer           bool
	indexPredicateConsumer  bool
	partitionConsumer       string
	checkDefinitionEquality bool
	visibilitySnapshot      bool
	catalogSnapshot         bool // Demanded inquiry nodes after STRICT folding.
	// Materialized definitions need frozen renderer metadata while retaining
	// their distinct, post-analysis bound-parameter diagnostic.
	materializedDefinition bool
}
