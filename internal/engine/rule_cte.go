package engine

import (
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// All original bodies have completed ordinary typed analysis and parameter
// inference. Rewrite modifiers before parent rules, without altering providers.
func (e *Engine) normalizeRuleCTEsLocked(scope *StatementScope, auth ruleAuthority, statement parser.Statement, b *analyze.Binding) error {
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
		program := ruleProgram{Count: -1, Returning: -1}
		if _, err := (ruleRewriter{e.catalog.owner()}).ExpandLocked(auth, definition.Statement, original, nil, &program); err != nil {
			return err
		}
		if err := RuleCTEProgramError(program); err != nil {
			return err
		}
		command := program.Commands[0]
		if b.Rule.CTEs == nil {
			b.Rule.CTEs = RuleCTEsSlotOf(make(map[*plan.CTEDefinition]*ruleCTECommand))
		}
		RuleCTEsOf(b.Rule.CTEs)[definition] = &ruleCTECommand{Definition: definition, Command: command, Caller: auth, Events: program.Events, Mutation: -1}
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

// Admit normalized actual targets before canonical CTE static preflight. The
// original parsed contexts remain present for caller/name/storage fences.
// Enter held; success retains Engine.mu, failure releases it.
func (e *Engine) admitRuleCTEsLocked(scope *StatementScope, statement parser.Statement, b *analyze.Binding) ([]ids.ObjectRef, error) {
	if len(RuleCTEsOf(b.Rule.CTEs)) == 0 {
		return nil, nil
	}
	// Prepared bindings retain analysis recipes, never an execution's admitted
	// index or refreshed principal. Each request owns these mutable tuples.
	commands := make(map[*plan.CTEDefinition]*ruleCTECommand, len(RuleCTEsOf(b.Rule.CTEs)))
	for definition, recipe := range RuleCTEsOf(b.Rule.CTEs) {
		copy := *recipe
		commands[definition] = &copy
	}
	b.Rule.CTEs = RuleCTEsSlotOf(commands)
	program := ruleProgram{}
	var normalized []*ruleCTECommand
	for _, definition := range b.CTEs {
		rewrite := RuleCTEsOf(b.Rule.CTEs)[definition]
		if rewrite == nil {
			continue
		}
		mutation, _ := analyze.MutationBinding(definition)
		if err := analyze.CheckBinding(ruleAuthorizationOf(rewrite.Caller), *mutation, definition.Statement); err != nil {
			e.mu.Unlock()
			return nil, err
		}
		if _, selectOnly := rewrite.Command.Syntax.(*parser.Select); selectOnly {
			continue
		}
		program.Commands = append(program.Commands, rewrite.Command)
		program.Events = append(program.Events, rewrite.Events...)
		normalized = append(normalized, rewrite)
	}
	refs, err := e.admitRuleCommandsLocked(scope, statement, &program)
	if err != nil {
		return nil, err
	}
	fresh, err := e.statementAuthorizationLocked(scope.op, statement)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	for _, rewrite := range RuleCTEsOf(b.Rule.CTEs) {
		caller := fresh
		caller.State.CurrentUser = rewrite.Caller.Access().State.CurrentUser
		if err := caller.Roles.ValidatePrincipal(caller.State.CurrentUser); err != nil {
			e.mu.Unlock()
			return nil, err
		}
		mutation, _ := analyze.MutationBinding(rewrite.Definition)
		if err := checkRowSecurityBinding(scope, caller, *mutation); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	for _, rewrite := range RuleCTEsOf(b.Rule.CTEs) {
		caller := fresh
		caller.State.CurrentUser = rewrite.Caller.Access().State.CurrentUser
		mutation, _ := analyze.MutationBinding(rewrite.Definition)
		if err := analyze.CheckBinding(caller, *mutation, rewrite.Definition.Statement); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	if len(scope.mutations) == 0 {
		scope.mutations = []statementMutation{scope.statementMutation}
		scope.activeMutation = 0
	}
	for i, rewrite := range normalized {
		rewrite.Command = program.Commands[i]
		if index := scope.mutationIndex(rewrite.Definition.Statement); rewrite.Command.Syntax == rewrite.Definition.Statement && index >= 0 {
			rewrite.Mutation = index
			continue
		}
		physical := PhysicalBinding(rewrite.Command.Binding)
		scope.mutations[scope.activeMutation] = scope.statementMutation
		rewrite.Mutation = len(scope.mutations)
		scope.mutations = append(scope.mutations, statementMutation{statement: rewrite.Command.Syntax, mutationTarget: mutationTarget{lockedRef: physical.Ref, lockedDescriptor: physical.Relation, lockedStorage: physical.Storage}, viewWrite: ViewWriteOf(rewrite.Command.Binding.ViewWrite)})
	}
	return refs, nil
}
