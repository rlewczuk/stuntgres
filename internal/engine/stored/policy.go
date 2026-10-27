package stored

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

type Policy struct {
	syntax       parser.Expr
	expression   expr.Expr
	presentation *View
}

// NewPolicy builds a frozen policy condition.
func NewPolicy(syntax parser.Expr, expression expr.Expr, presentation *View) *Policy {
	return &Policy{syntax: syntax, expression: expression, presentation: presentation}
}

// Syntax returns the condition's syntax.
func (e *Policy) Syntax() parser.Expr { return e.syntax }

// Expression returns the analyzed condition.
func (e *Policy) Expression() expr.Expr { return e.expression }

// Presentation returns the frozen definition that renders the condition.
func (e *Policy) Presentation() *View { return e.presentation }

// PolicyOf returns p's concrete condition, or nil when p is absent.
func PolicyOf(p catalog.PolicyPayload) *Policy {
	e, _ := p.(*Policy)
	return e
}

func (e *Policy) DependsOn(target ids.ObjectAddress) bool {
	return e.presentation.DependsOn(target)
}

func (e *Policy) Validate(v catalog.Read, owner *catalog.Relation) error {
	return ValidatePolicy(v, owner, e)
}

func (e *Policy) VisitExpression(visit func(expr.Expr) error) error {
	return visit(e.expression)
}

func PolicySQL(ctx context.Context, view catalog.Read, expression *Policy) (string, error) {
	r := NewQueryRenderer(expression.presentation)
	r.Frame = 1
	source := analyze.ViewFromRoot(r.frames[1])
	leaf := r.sources[0]
	d, ok := view.Relation(leaf.Ref.Address)
	if !ok {
		return "", fmt.Errorf("stuntgres: policy owner is missing")
	}
	r.reserved[d.Name()] = true
	// Replay keeps logical source aliases, not positional bridge names that
	// would become user-visible aliases when Restore binds this definition.
	// Expression/literal rendering remains in the ordinary replay mode.
	r.policyReplaySources = true
	root := expression.presentation.query.Root.(*plan.ProjectNode)
	names := ruleRangeTable{used: map[string]int{d.Name(): 0}}
	r.ruleTables = map[uint32]ruleRangeTable{root.Frame: names}
	r.ruleRootNames = &names
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
