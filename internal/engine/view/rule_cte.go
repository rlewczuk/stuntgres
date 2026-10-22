package view

import (
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// The original definition remains the provider key and SQL/output authority.
// Only this execution-private tuple chooses the executable command/context.
type RuleCTECommand struct {
	Definition *plan.CTEDefinition
	Command    RuleCommand
	Caller     RuleAuthority
	Events     []catalog.ExpressionDependency
	Mutation   int
}

func RuleCTEProgramError(program RuleProgram) error {
	if len(program.Commands) == 1 {
		return nil
	}
	if len(program.Commands) == 0 {
		return sqlError("0A000", "DO INSTEAD NOTHING rules are not supported for data-modifying statements in WITH")
	}
	for _, command := range program.Commands {
		switch command.Source {
		case RuleConditionalInstead:
			return sqlError("0A000", "conditional DO INSTEAD rules are not supported for data-modifying statements in WITH")
		case RuleAlso:
			return sqlError("0A000", "DO ALSO rules are not supported for data-modifying statements in WITH")
		}
	}
	return sqlError("0A000", "multi-statement DO INSTEAD rules are not supported for data-modifying statements in WITH")
}

// All original bodies have completed ordinary typed analysis and parameter
// inference. Rewrite modifiers before parent rules, without altering providers.
// Engine.mu is held, as RuleRewriter.ExpandLocked needs.
func NormalizeRuleCTEsLocked(owner *catalog.Owner, auth RuleAuthority, statement parser.Statement, b *analyze.Binding) error {
	matched := func(relation *catalog.Relation, statement parser.Statement) bool {
		return relation != nil && slices.ContainsFunc(stored.RulesOf(relation), func(rule stored.Rule) bool { return rule.Event() == stored.RuleEvent(statement) })
	}
	needed := b.Rule.Template || matched(BindingRuleRelation(*b), statement)
	for _, definition := range b.CTEs {
		if mutation, ok := analyze.MutationBinding(definition); ok && matched(BindingRuleRelation(*mutation), definition.Statement) {
			needed = true
			break
		}
	}
	if !needed {
		return nil
	}
	for _, definition := range b.CTEs {
		mutation, ok := analyze.MutationBinding(definition)
		if !ok || RuleCTEsOf(b.Rule.CTEs)[definition] != nil {
			continue
		}
		original := *mutation
		original.Parameters = b.Parameters
		original.CTEs = b.CTEs
		program := RuleProgram{Count: -1, Returning: -1}
		if _, err := (RuleRewriter{Owner: owner}).ExpandLocked(auth, definition.Statement, original, nil, &program); err != nil {
			return err
		}
		if err := RuleCTEProgramError(program); err != nil {
			return err
		}
		command := program.Commands[0]
		if b.Rule.CTEs == nil {
			b.Rule.CTEs = RuleCTEsSlotOf(make(map[*plan.CTEDefinition]*RuleCTECommand))
		}
		RuleCTEsOf(b.Rule.CTEs)[definition] = &RuleCTECommand{Definition: definition, Command: command, Caller: auth, Events: program.Events, Mutation: -1}
		b.Dependencies = append(b.Dependencies, program.Events...)
		if command.Binding.Relation != nil {
			b.Dependencies = append(b.Dependencies, catalog.ExpressionDependency{Ref: command.Binding.Ref, Revision: command.Binding.Relation.Revision()})
		}
		b.Dependencies = append(b.Dependencies, command.Binding.Dependencies...)
		b.Prepared.SequenceConstants = append(b.Prepared.SequenceConstants, command.Binding.Prepared.SequenceConstants...)
		b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, command.Binding.Prepared.RegtypeConstants...)
	}
	return nil
}
