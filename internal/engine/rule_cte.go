package engine

import (
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// All original bodies have completed ordinary typed analysis and parameter
// inference. Rewrite modifiers before parent rules, without altering providers.
func (e *Engine) normalizeRuleCTEsLocked(scope *StatementScope, auth view.RuleAuthority, statement parser.Statement, b *analyze.Binding) error {
	matched := func(relation *catalog.Relation, statement parser.Statement) bool {
		return relation != nil && slices.ContainsFunc(stored.RulesOf(relation), func(rule stored.Rule) bool { return rule.Event() == stored.RuleEvent(statement) })
	}
	needed := b.Rule.Template || matched(view.BindingRuleRelation(*b), statement)
	for _, definition := range b.CTEs {
		if mutation, ok := analyze.MutationBinding(definition); ok && matched(view.BindingRuleRelation(*mutation), definition.Statement) {
			needed = true
			break
		}
	}
	if !needed {
		return nil
	}
	for _, definition := range b.CTEs {
		mutation, ok := analyze.MutationBinding(definition)
		if !ok || view.RuleCTEsOf(b.Rule.CTEs)[definition] != nil {
			continue
		}
		original := *mutation
		original.Parameters = b.Parameters
		original.CTEs = b.CTEs
		program := view.RuleProgram{Count: -1, Returning: -1}
		if _, err := (view.RuleRewriter{Owner: e.catalog.owner()}).ExpandLocked(auth, definition.Statement, original, nil, &program); err != nil {
			return err
		}
		if err := view.RuleCTEProgramError(program); err != nil {
			return err
		}
		command := program.Commands[0]
		if b.Rule.CTEs == nil {
			b.Rule.CTEs = view.RuleCTEsSlotOf(make(map[*plan.CTEDefinition]*view.RuleCTECommand))
		}
		view.RuleCTEsOf(b.Rule.CTEs)[definition] = &view.RuleCTECommand{Definition: definition, Command: command, Caller: auth, Events: program.Events, Mutation: -1}
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
	if len(view.RuleCTEsOf(b.Rule.CTEs)) == 0 {
		return nil, nil
	}
	// Prepared bindings retain analysis recipes, never an execution's admitted
	// index or refreshed principal. Each request owns these mutable tuples.
	commands := make(map[*plan.CTEDefinition]*view.RuleCTECommand, len(view.RuleCTEsOf(b.Rule.CTEs)))
	for definition, recipe := range view.RuleCTEsOf(b.Rule.CTEs) {
		copy := *recipe
		commands[definition] = &copy
	}
	b.Rule.CTEs = view.RuleCTEsSlotOf(commands)
	program := view.RuleProgram{}
	var normalized []*view.RuleCTECommand
	for _, definition := range b.CTEs {
		rewrite := view.RuleCTEsOf(b.Rule.CTEs)[definition]
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
	for _, rewrite := range view.RuleCTEsOf(b.Rule.CTEs) {
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
	for _, rewrite := range view.RuleCTEsOf(b.Rule.CTEs) {
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
		physical := view.PhysicalBinding(rewrite.Command.Binding)
		scope.mutations[scope.activeMutation] = scope.statementMutation
		rewrite.Mutation = len(scope.mutations)
		scope.mutations = append(scope.mutations, statementMutation{statement: rewrite.Command.Syntax, mutationTarget: mutationTarget{lockedRef: physical.Ref, lockedDescriptor: physical.Relation, lockedStorage: physical.Storage}, Command: dml.Command{ViewWrite: view.ViewWriteOf(rewrite.Command.Binding.ViewWrite)}})
	}
	return refs, nil
}
