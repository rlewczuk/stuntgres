package view

import (
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// FreezeRoutineAction shares checked DML/query presentation with rule actions,
// without rule OLD/NEW parameters or a relation-owned rewrite definition.
func FreezeRoutineAction(read catalog.Read, owner ids.SessionToken, syntax parser.Statement, binding analyze.Binding) (stored.RuleAction, error) {
	presentation, err := freezeRuleActionPresentation(read, owner, syntax, binding)
	if err != nil {
		return stored.RuleAction{}, err
	}
	modifiers, err := freezeRuleModifierPresentations(read, owner, binding)
	if err != nil {
		return stored.RuleAction{}, err
	}
	presentation = presentation.With(func(p *stored.ViewParts) { p.RoutineInputs = slices.Clone(binding.Parameters.Types) })
	for statement, part := range modifiers {
		modifiers[statement] = part.With(func(p *stored.ViewParts) { p.RoutineInputs = slices.Clone(binding.Parameters.Types) })
	}
	binding.Storage = nil
	binding.Resolvers = analyze.ResolverInputs{}
	binding.QuerySources = slices.Clone(binding.QuerySources)
	for i := range binding.QuerySources {
		binding.QuerySources[i].Storage = nil
	}
	return stored.NewRuleAction(stored.RuleActionParts{Syntax: syntax, Binding: binding, Presentation: presentation, ModifierPresentations: modifiers}), nil
}
