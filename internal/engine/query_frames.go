package engine

import (
	"context"
	"stuntgres/internal/parser"
)

// Lexical scopes share statement authority and parameter/dependency ownership.
// Only query-local namespace and aggregate state is saved at a query boundary.
type queryBindingScope struct {
	relation            *relationDescriptor
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
	rowDiagnosticMode   relationLockMode
	windowRaw           []parser.WindowDefinition
	windowDefinitions   map[string]boundWindowSpec
	windowCalls         []windowCall
	argumentOwner       argumentWindowOwner
	stringFromForbidden bool
}

func (b *expressionBinder) frameID() uint32 {
	if b.queryFrame == 0 {
		return 1
	}
	return b.queryFrame
}

func (b *expressionBinder) enterQuery(disallowParent bool) func() {
	oldSetGuard, oldForbidden := b.stringSetGuard, b.stringFromForbidden
	oldRegexpClause := b.regexpClause
	oldTargetSet := b.queryHasTargetSet
	oldFromSet := b.fromSetObservation
	b.fromSetObservation = fromSetObservation{}
	b.queryHasTargetSet = false
	b.stringSetGuard, b.stringFromForbidden = "", false
	b.regexpClause = ""
	oldNamespace := b.queryNamespace
	parent := queryBindingScope{namespace: b.queryNamespace, hidden: b.queryHidden, frame: b.frameID(), allowed: !disallowParent, clause: b.aggregateClause, aggregates: b.aggregates, returning: b.returningAliases, nonLateral: b.queryNonLateral}
	parent.stringFromForbidden = oldForbidden
	parent.relation = b.relation
	parent.groupingActive, parent.groupSets = b.groupingActive, b.groupSets
	parent.rowDiagnosticMode = b.queryRowDiagnosticMode
	parent.windowRaw, parent.windowDefinitions, parent.windowCalls = b.windowRaw, b.windowDefinitions, b.windowCalls
	parent.argumentOwner = b.argumentOwner
	if parent.namespace == nil && b.relation != nil && (b.updateParameters || b.deleteTarget) {
		ns := b.mutationTargetNamespace()
		parent.namespace = &ns
	}
	oldFrame := b.queryFrame
	if b.nextQueryFrame == 0 {
		b.nextQueryFrame = 1
	}
	if oldFrame != 0 || b.relation != nil {
		b.nextQueryFrame++
		b.queryFrame = b.nextQueryFrame
	} else {
		b.queryFrame = 1
	}
	index := len(b.queryScopes)
	b.queryScopes = append(b.queryScopes, parent)
	b.queryNamespace, b.queryHidden = nil, nil
	b.aggregates, b.aggregateClause = nil, ""
	b.groupingActive, b.groupSets = false, nil
	b.windowRaw, b.windowDefinitions, b.windowCalls = nil, nil, nil
	b.argumentOwner = argumentWindowOwner{}
	b.returningAliases = nil
	b.queryNonLateral = disallowParent
	b.queryRowDiagnosticMode = 0
	return func() {
		b.stringSetGuard, b.stringFromForbidden = oldSetGuard, oldForbidden
		b.regexpClause = oldRegexpClause
		b.queryHasTargetSet = oldTargetSet
		b.fromSetObservation = oldFromSet
		parent := b.queryScopes[index]
		b.queryScopes = b.queryScopes[:index]
		b.queryNamespace, b.queryHidden = oldNamespace, parent.hidden
		b.aggregates, b.aggregateClause = parent.aggregates, parent.clause
		b.groupingActive, b.groupSets = parent.groupingActive, parent.groupSets
		b.windowRaw, b.windowDefinitions, b.windowCalls = parent.windowRaw, parent.windowDefinitions, parent.windowCalls
		b.argumentOwner = parent.argumentOwner
		b.returningAliases, b.queryNonLateral = parent.returning, parent.nonLateral
		b.queryRowDiagnosticMode = parent.rowDiagnosticMode
		b.queryFrame = oldFrame
	}
}

func (s *queryState) evaluation(ctx context.Context) EvalContext {
	s.inheritCTEs()
	frame := s.frame
	if frame == 0 {
		frame = 1
	}
	evaluation := EvalContext{Scope: 1, Frame: frame, Parameters: s.parameters, Input: s.input, Output: s.output, Outer: s.outer, query: s}
	if s.functionRecheck != nil {
		evaluation.queryCurrentRows = s.functionRecheck.rows
	}
	if s.outer != nil {
		evaluation.catalog = s.outer.catalog
	}
	if s.source != nil && s.source.view != nil {
		evaluation.catalog = s.source.view.Catalog()
	}
	if s.source != nil && s.source.routineRecipe != nil {
		recipe := s.source.routineRecipe
		evaluation.routineRecipe = true
		evaluation.routineChecks, evaluation.routineConsumer = recipe.view.dependencies, recipe
	}
	evaluation.setContext(ctx)
	return evaluation
}

func (b *expressionBinder) outerQualifiers() []queryQualifier {
	var out []queryQualifier
	for i := len(b.queryScopes) - 1; i >= 0; i-- {
		s := &b.queryScopes[i]
		if len(s.hidden) != 0 {
			out = append(out, s.hidden...)
		} else if s.namespace != nil {
			out = append(out, s.namespace.qualifiers...)
		}
	}
	return out
}

func (b *expressionBinder) queryColumnInFrame(c queryColumn, pos parser.Position, frame uint32) BoundExpr {
	expr := b.queryColumnAt(c, pos).(columnExpression)
	expr.frame = frame
	expr.mergeSource = c.leaf != queryTargetLeaf && b.mergeReturningFrame(frame)
	return expr
}

func (b *expressionBinder) queryTargetRelation() *relationDescriptor {
	if b.relation != nil {
		return b.relation
	}
	for i := len(b.queryScopes) - 1; i >= 0; i-- {
		if d := b.queryScopes[i].relation; d != nil {
			return d
		}
	}
	return nil
}
