package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// RuleParts mirrors every ruleDefinition field. NewRule builds a rule from
// it; Parts and With copy a rule out and back without changing it.
type RuleParts struct {
	OID                   ids.OID
	Name                  string
	Event                 string
	Instead               bool
	Condition             expr.Expr
	ConditionPresentation *viewDefinition
	ConditionBinding      *analyze.Binding
	Actions               []ruleAction
	References            []ids.ObjectAddress
}

// NewRule builds a rule from p.
func NewRule(p RuleParts) ruleDefinition {
	return ruleDefinition{
		OID:                   p.OID,
		Name:                  p.Name,
		event:                 p.Event,
		instead:               p.Instead,
		condition:             p.Condition,
		conditionPresentation: p.ConditionPresentation,
		conditionBinding:      p.ConditionBinding,
		actions:               p.Actions,
		references:            p.References,
	}
}

// Parts returns r's fields.
func (r ruleDefinition) Parts() RuleParts {
	return RuleParts{
		OID:                   r.OID,
		Name:                  r.Name,
		Event:                 r.event,
		Instead:               r.instead,
		Condition:             r.condition,
		ConditionPresentation: r.conditionPresentation,
		ConditionBinding:      r.conditionBinding,
		Actions:               r.actions,
		References:            r.references,
	}
}

// With returns r's parts after edit as a new rule. It never changes r, so a
// published rule list stays immutable.
func (r ruleDefinition) With(edit func(*RuleParts)) ruleDefinition {
	p := r.Parts()
	edit(&p)
	return NewRule(p)
}

// Event returns the rule's event: SELECT, INSERT, UPDATE or DELETE.
func (r ruleDefinition) Event() string { return r.event }

// Instead reports whether the rule is a DO INSTEAD rule.
func (r ruleDefinition) Instead() bool { return r.instead }

// Condition returns the analyzed WHERE condition, or nil.
func (r ruleDefinition) Condition() expr.Expr { return r.condition }

// ConditionPresentation returns the frozen definition that renders the
// condition, or nil.
func (r ruleDefinition) ConditionPresentation() *viewDefinition {
	return r.conditionPresentation
}

// ConditionBinding returns the condition's binding, or nil.
func (r ruleDefinition) ConditionBinding() *analyze.Binding { return r.conditionBinding }

// Actions returns the rule's actions; callers never write through it.
func (r ruleDefinition) Actions() []ruleAction { return r.actions }

// References returns the objects the rule depends on.
func (r ruleDefinition) References() []ids.ObjectAddress { return r.references }

// RuleActionParts mirrors every ruleAction field, like RuleParts.
type RuleActionParts struct {
	Syntax                parser.Statement
	Binding               analyze.Binding
	Presentation          *viewDefinition
	ModifierPresentations map[parser.Statement]*viewDefinition
}

// NewRuleAction builds a rule action from p.
func NewRuleAction(p RuleActionParts) ruleAction {
	return ruleAction{
		syntax:                p.Syntax,
		binding:               p.Binding,
		presentation:          p.Presentation,
		modifierPresentations: p.ModifierPresentations,
	}
}

// Parts returns a's fields.
func (a ruleAction) Parts() RuleActionParts {
	return RuleActionParts{
		Syntax:                a.syntax,
		Binding:               a.binding,
		Presentation:          a.presentation,
		ModifierPresentations: a.modifierPresentations,
	}
}

// With returns a's parts after edit as a new action; a is not changed.
func (a ruleAction) With(edit func(*RuleActionParts)) ruleAction {
	p := a.Parts()
	edit(&p)
	return NewRuleAction(p)
}

// Syntax returns the action statement.
func (a ruleAction) Syntax() parser.Statement { return a.syntax }

// Binding returns the action's analyzed template.
func (a ruleAction) Binding() analyze.Binding { return a.binding }

// Presentation returns the frozen definition that renders the action.
func (a ruleAction) Presentation() *viewDefinition { return a.presentation }

// ModifierPresentations returns the frozen definitions of the action's
// data-modifying WITH queries.
func (a ruleAction) ModifierPresentations() map[parser.Statement]*viewDefinition {
	return a.modifierPresentations
}
