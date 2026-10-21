package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type policyExpression struct {
	syntax       parser.Expr
	expression   expr.Expr
	presentation *viewDefinition
}

// policyExpressionOf returns p's concrete condition, or nil when p is absent.
func policyExpressionOf(p catalog.PolicyPayload) *policyExpression {
	e, _ := p.(*policyExpression)
	return e
}

func (e *policyExpression) DependsOn(target ids.ObjectAddress) bool {
	return e.presentation.DependsOn(target)
}

func (e *policyExpression) Validate(v catalog.Read, owner *catalog.Relation) error {
	return validatePolicyExpression(v, owner, e)
}

func (e *policyExpression) VisitExpression(visit func(expr.Expr) error) error {
	return visit(e.expression)
}

func policyExpressionSQL(ctx context.Context, view catalog.Read, expression *policyExpression) (string, error) {
	r := newViewRenderer(expression.presentation)
	r.frame = 1
	source := analyze.ViewFromRoot(r.frames[1])
	leaf := r.sources[0]
	d, ok := view.Relation(leaf.ref.Address)
	if !ok {
		return "", fmt.Errorf("stuntgres: policy owner is missing")
	}
	r.reserved[d.Name()] = true
	slots := make(map[int]string, len(leaf.attributes))
	for i, attribute := range leaf.attributes {
		for j, current := range d.Attributes {
			if current == attribute {
				slots[i] = catalog.QuoteIdentifier(d.Name()) + "." + catalog.QuoteIdentifier(d.Columns[j].Name)
				break
			}
		}
	}
	r.slots[source] = slots
	return r.expressionSQL(ctx, view, expression.syntax, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
}
