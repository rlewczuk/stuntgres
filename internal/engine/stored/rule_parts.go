package stored

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// RuleParts mirrors every Rule field. NewRule builds a rule from
// it; Parts and With copy a rule out and back without changing it.
type RuleParts struct {
	OID                   ids.OID
	Name                  string
	Event                 string
	Instead               bool
	Condition             expr.Expr
	ConditionPresentation *View
	ConditionBinding      *analyze.Binding
	Actions               []RuleAction
	References            []ids.ObjectAddress
}

// NewRule builds a rule from p.
func NewRule(p RuleParts) Rule {
	return Rule{
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
func (r Rule) Parts() RuleParts {
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
func (r Rule) With(edit func(*RuleParts)) Rule {
	p := r.Parts()
	edit(&p)
	return NewRule(p)
}

// Event returns the rule's event: SELECT, INSERT, UPDATE or DELETE.
func (r Rule) Event() string { return r.event }

// Instead reports whether the rule is a DO INSTEAD rule.
func (r Rule) Instead() bool { return r.instead }

// Condition returns the analyzed WHERE condition, or nil.
func (r Rule) Condition() expr.Expr { return r.condition }

// ConditionPresentation returns the frozen definition that renders the
// condition, or nil.
func (r Rule) ConditionPresentation() *View {
	return r.conditionPresentation
}

// ConditionBinding returns the condition's binding, or nil.
func (r Rule) ConditionBinding() *analyze.Binding { return r.conditionBinding }

// Actions returns the rule's actions; callers never write through it.
func (r Rule) Actions() []RuleAction { return r.actions }

// References returns the objects the rule depends on.
func (r Rule) References() []ids.ObjectAddress { return r.references }

// RuleActionParts mirrors every RuleAction field, like RuleParts.
type RuleActionParts struct {
	Syntax                parser.Statement
	Binding               analyze.Binding
	Presentation          *View
	ModifierPresentations map[parser.Statement]*View
}

// NewRuleAction builds a rule action from p.
func NewRuleAction(p RuleActionParts) RuleAction {
	return RuleAction{
		syntax:                p.Syntax,
		binding:               p.Binding,
		presentation:          p.Presentation,
		modifierPresentations: p.ModifierPresentations,
	}
}

// Parts returns a's fields.
func (a RuleAction) Parts() RuleActionParts {
	return RuleActionParts{
		Syntax:                a.syntax,
		Binding:               a.binding,
		Presentation:          a.presentation,
		ModifierPresentations: a.modifierPresentations,
	}
}

// With returns a's parts after edit as a new action; a is not changed.
func (a RuleAction) With(edit func(*RuleActionParts)) RuleAction {
	p := a.Parts()
	edit(&p)
	return NewRuleAction(p)
}

// Syntax returns the action statement.
func (a RuleAction) Syntax() parser.Statement { return a.syntax }

// Binding returns the action's analyzed template.
func (a RuleAction) Binding() analyze.Binding { return a.binding }

// Presentation returns the frozen definition that renders the action.
func (a RuleAction) Presentation() *View { return a.presentation }

// ModifierPresentations returns the frozen definitions of the action's
// data-modifying WITH queries.
func (a RuleAction) ModifierPresentations() map[parser.Statement]*View {
	return a.modifierPresentations
}
