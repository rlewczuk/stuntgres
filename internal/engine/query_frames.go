package engine

import (
	"context"
	"stuntgres/internal/parser"
)

// Lexical scopes share statement authority and parameter/dependency ownership.
// Only query-local namespace and aggregate state is saved at a query boundary.
type queryBindingScope struct {
	relation          *relationDescriptor
	namespace         *queryNamespace
	hidden            []queryQualifier
	frame             uint32
	allowed           bool
	clause            string
	aggregates        []aggregateCall
	returning         *returningAliases
	nonLateral        bool
	rowDiagnosticMode relationLockMode
}

func (b *expressionBinder) frameID() uint32 {
	if b.queryFrame == 0 {
		return 1
	}
	return b.queryFrame
}

func (b *expressionBinder) enterQuery(disallowParent bool) func() {
	oldNamespace := b.queryNamespace
	parent := queryBindingScope{namespace: b.queryNamespace, hidden: b.queryHidden, frame: b.frameID(), allowed: !disallowParent, clause: b.aggregateClause, aggregates: b.aggregates, returning: b.returningAliases, nonLateral: b.queryNonLateral}
	parent.relation = b.relation
	parent.rowDiagnosticMode = b.queryRowDiagnosticMode
	if parent.namespace == nil && b.relation != nil && (b.updateParameters || b.deleteTarget) {
		ns := b.updateTargetNamespace()
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
	b.returningAliases = nil
	b.queryNonLateral = disallowParent
	b.queryRowDiagnosticMode = 0
	return func() {
		parent := b.queryScopes[index]
		b.queryScopes = b.queryScopes[:index]
		b.queryNamespace, b.queryHidden = oldNamespace, parent.hidden
		b.aggregates, b.aggregateClause = parent.aggregates, parent.clause
		b.returningAliases, b.queryNonLateral = parent.returning, parent.nonLateral
		b.queryRowDiagnosticMode = parent.rowDiagnosticMode
		b.queryFrame = oldFrame
	}
}

func (s *queryState) evaluation(ctx context.Context) EvalContext {
	frame := s.frame
	if frame == 0 {
		frame = 1
	}
	return EvalContext{Context: ctx, Scope: 1, Frame: frame, Parameters: s.parameters, statementScope: s.source.scope, Input: s.input, Output: s.output, Outer: s.outer, query: s}
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
