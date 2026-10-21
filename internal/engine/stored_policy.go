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

// NewPolicy builds a frozen policy condition.
func NewPolicy(syntax parser.Expr, expression expr.Expr, presentation *viewDefinition) *policyExpression {
	return &policyExpression{syntax: syntax, expression: expression, presentation: presentation}
}

// Syntax returns the condition's syntax.
func (e *policyExpression) Syntax() parser.Expr { return e.syntax }

// Expression returns the analyzed condition.
func (e *policyExpression) Expression() expr.Expr { return e.expression }

// Presentation returns the frozen definition that renders the condition.
func (e *policyExpression) Presentation() *viewDefinition { return e.presentation }

// policyExpressionOf returns p's concrete condition, or nil when p is absent.
func PolicyOf(p catalog.PolicyPayload) *policyExpression {
	e, _ := p.(*policyExpression)
	return e
}

func (e *policyExpression) DependsOn(target ids.ObjectAddress) bool {
	return e.presentation.DependsOn(target)
}

func (e *policyExpression) Validate(v catalog.Read, owner *catalog.Relation) error {
	return ValidatePolicy(v, owner, e)
}

func (e *policyExpression) VisitExpression(visit func(expr.Expr) error) error {
	return visit(e.expression)
}

func PolicySQL(ctx context.Context, view catalog.Read, expression *policyExpression) (string, error) {
	r := NewQueryRenderer(expression.presentation)
	r.Frame = 1
	source := analyze.ViewFromRoot(r.frames[1])
	leaf := r.sources[0]
	d, ok := view.Relation(leaf.Ref.Address)
	if !ok {
		return "", fmt.Errorf("stuntgres: policy owner is missing")
	}
	r.reserved[d.Name()] = true
	slots := make(map[int]string, len(leaf.Attributes))
	for i, attribute := range leaf.Attributes {
		for j, current := range d.Attributes {
			if current == attribute {
				slots[i] = catalog.QuoteIdentifier(d.Name()) + "." + catalog.QuoteIdentifier(d.Columns[j].Name)
				break
			}
		}
	}
	r.Slots[source] = slots
	return r.ExpressionSQL(ctx, view, expression.syntax, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
}
