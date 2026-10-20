package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Lexical scopes share statement authority and parameter/dependency ownership.
// Only query-local namespace and aggregate state is saved at a query boundary.
type queryBindingScope struct {
	relation            *catalog.Relation
	namespace           *queryNamespace
	hidden              []queryQualifier
	frame               uint32
	allowed             bool
	clause              string
	aggregates          []aggregateCall
	groupingActive      bool
	groupSets           [][]int
	returning           *returningAliases
	nonLateral          bool
	rowDiagnosticMode   lock.Mode
	windowRaw           []parser.WindowDefinition
	windowDefinitions   map[string]boundWindowSpec
	windowCalls         []windowCall
	argumentOwner       argumentWindowOwner
	stringFromForbidden bool
}

func (b *expressionBinder) frameID() uint32 {
	if b.query.frame == 0 {
		return 1
	}
	return b.query.frame
}

func (b *expressionBinder) enterQuery(disallowParent bool) func() {
	oldSetGuard, oldForbidden := b.clause.stringSetGuard, b.clause.stringFromForbidden
	oldRegexpClause := b.clause.regexpClause
	oldTargetSet := b.clause.queryHasTargetSet
	oldFromSet := b.clause.fromSetObservation
	b.clause.fromSetObservation = fromSetObservation{}
	b.clause.queryHasTargetSet = false
	b.clause.stringSetGuard, b.clause.stringFromForbidden = "", false
	b.clause.regexpClause = ""
	oldNamespace := b.query.namespace
	parent := queryBindingScope{namespace: b.query.namespace, hidden: b.query.hidden, frame: b.frameID(), allowed: !disallowParent, clause: b.clause.aggregateClause, aggregates: b.grouping.aggregates, returning: b.target.returningAliases, nonLateral: b.query.nonLateral}
	parent.stringFromForbidden = oldForbidden
	parent.relation = b.target.relation
	parent.groupingActive, parent.groupSets = b.grouping.active, b.grouping.groupSets
	parent.rowDiagnosticMode = b.query.rowDiagnosticMode
	parent.windowRaw, parent.windowDefinitions, parent.windowCalls = b.grouping.windowRaw, b.grouping.windowDefinitions, b.grouping.windowCalls
	parent.argumentOwner = b.grouping.argumentOwner
	// Only the initial mutation owner can supply this fallback. Before a
	// nested SELECT's FROM is bound, its nil namespace must not impersonate
	// the retained mutation relation in an intermediate query frame.
	if parent.namespace == nil && b.query.frame == 0 && b.target.relation != nil && (b.params.updateParameters || b.target.deleteTarget) {
		ns := b.mutationTargetNamespace()
		parent.namespace = &ns
	}
	oldFrame := b.query.frame
	if b.query.nextQueryFrame == 0 {
		b.query.nextQueryFrame = 1
	}
	if oldFrame != 0 || b.target.relation != nil {
		b.query.nextQueryFrame++
		b.query.frame = b.query.nextQueryFrame
	} else {
		b.query.frame = 1
	}
	index := len(b.query.scopes)
	b.query.scopes = append(b.query.scopes, parent)
	b.query.namespace, b.query.hidden = nil, nil
	b.grouping.aggregates, b.clause.aggregateClause = nil, ""
	b.grouping.active, b.grouping.groupSets = false, nil
	b.grouping.windowRaw, b.grouping.windowDefinitions, b.grouping.windowCalls = nil, nil, nil
	b.grouping.argumentOwner = argumentWindowOwner{}
	b.target.returningAliases = nil
	b.query.nonLateral = disallowParent
	b.query.rowDiagnosticMode = 0
	return func() {
		b.clause.stringSetGuard, b.clause.stringFromForbidden = oldSetGuard, oldForbidden
		b.clause.regexpClause = oldRegexpClause
		b.clause.queryHasTargetSet = oldTargetSet
		b.clause.fromSetObservation = oldFromSet
		parent := b.query.scopes[index]
		b.query.scopes = b.query.scopes[:index]
		b.query.namespace, b.query.hidden = oldNamespace, parent.hidden
		b.grouping.aggregates, b.clause.aggregateClause = parent.aggregates, parent.clause
		b.grouping.active, b.grouping.groupSets = parent.groupingActive, parent.groupSets
		b.grouping.windowRaw, b.grouping.windowDefinitions, b.grouping.windowCalls = parent.windowRaw, parent.windowDefinitions, parent.windowCalls
		b.grouping.argumentOwner = parent.argumentOwner
		b.target.returningAliases, b.query.nonLateral = parent.returning, parent.nonLateral
		b.query.rowDiagnosticMode = parent.rowDiagnosticMode
		b.query.frame = oldFrame
	}
}

func (s *queryState) evaluation(ctx context.Context) EvalContext {
	s.inheritCTEs()
	frame := s.frame
	if frame == 0 {
		frame = 1
	}
	evaluation := EvalContext{evalFrame: evalFrame{Scope: 1, Frame: frame, Outer: s.outer, query: s}, evalStatement: evalStatement{Parameters: s.parameters, Input: s.input, Output: s.output}}
	if s.outer != nil {
		evaluation.catalog = s.outer.catalog
	}
	if s.source != nil && s.source.view != nil {
		evaluation.catalog = s.source.view.Catalog()
	}
	if s.source != nil && s.source.routineRecipe != nil {
		recipe := s.source.routineRecipe
		evaluation.routineRecipe = true
		evaluation.routineChecks, evaluation.routineConsumer = viewOf(recipe).dependencies, recipe
	}
	evaluation.setContext(ctx)
	return evaluation
}

func (b *expressionBinder) outerQualifiers() []queryQualifier {
	var out []queryQualifier
	for i := len(b.query.scopes) - 1; i >= 0; i-- {
		s := &b.query.scopes[i]
		if len(s.hidden) != 0 {
			out = append(out, s.hidden...)
		} else if s.namespace != nil {
			out = append(out, s.namespace.qualifiers...)
		}
	}
	return out
}

func (b *expressionBinder) queryColumnInFrame(c queryColumn, pos parser.Position, frame uint32) expr.Expr {
	expr := b.queryColumnAt(c, pos).(columnExpression)
	expr.frame = frame
	expr.mergeSource = c.leaf != queryTargetLeaf && b.mergeReturningFrame(frame)
	return expr
}

func (b *expressionBinder) queryTargetRelation() *catalog.Relation {
	if b.target.relation != nil {
		return b.target.relation
	}
	for i := len(b.query.scopes) - 1; i >= 0; i-- {
		if d := b.query.scopes[i].relation; d != nil {
			return d
		}
	}
	return nil
}
