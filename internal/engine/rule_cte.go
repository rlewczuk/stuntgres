package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// The original definition remains the provider key and SQL/output authority.
// Only this execution-private tuple chooses the executable command/context.
type ruleCTECommand struct {
	definition *plan.CTEDefinition
	command    ruleCommand
	caller     AuthorizationView
	events     []catalog.ExpressionDependency
	mutation   int
}

func ruleCTEProgramError(program ruleProgram) error {
	if len(program.commands) == 1 {
		return nil
	}
	if len(program.commands) == 0 {
		return sqlError("0A000", "DO INSTEAD NOTHING rules are not supported for data-modifying statements in WITH")
	}
	for _, command := range program.commands {
		switch command.source {
		case ruleConditionalInstead:
			return sqlError("0A000", "conditional DO INSTEAD rules are not supported for data-modifying statements in WITH")
		case ruleAlso:
			return sqlError("0A000", "DO ALSO rules are not supported for data-modifying statements in WITH")
		}
	}
	return sqlError("0A000", "multi-statement DO INSTEAD rules are not supported for data-modifying statements in WITH")
}

// All original bodies have completed ordinary typed analysis and parameter
// inference. Rewrite modifiers before parent rules, without altering providers.
func (e *Engine) normalizeRuleCTEsLocked(scope *StatementScope, auth AuthorizationView, statement parser.Statement, b *binding) error {
	matched := func(relation *catalog.Relation, statement parser.Statement) bool {
		return relation != nil && slices.ContainsFunc(rulesOf(relation), func(rule ruleDefinition) bool { return rule.event == ruleEvent(statement) })
	}
	needed := b.Rule.Template || matched(bindingRuleRelation(*b), statement)
	for _, definition := range b.CTEs {
		if mutation, ok := mutationBinding(definition); ok && matched(bindingRuleRelation(*mutation), definition.Statement) {
			needed = true
			break
		}
	}
	if !needed {
		return nil
	}
	for _, definition := range b.CTEs {
		mutation, ok := mutationBinding(definition)
		if !ok || ruleCTEsOf(b.Rule.CTEs)[definition] != nil {
			continue
		}
		original := *mutation
		original.Parameters = b.Parameters
		original.CTEs = b.CTEs
		program := ruleProgram{count: -1, returning: -1}
		if _, err := e.expandRulesLocked(scope, auth, definition.Statement, original, nil, &program); err != nil {
			return err
		}
		if err := ruleCTEProgramError(program); err != nil {
			return err
		}
		command := program.commands[0]
		if b.Rule.CTEs == nil {
			b.Rule.CTEs = ruleCTEsSlotOf(make(map[*plan.CTEDefinition]*ruleCTECommand))
		}
		ruleCTEsOf(b.Rule.CTEs)[definition] = &ruleCTECommand{definition: definition, command: command, caller: auth, events: program.events, mutation: -1}
		b.Dependencies = append(b.Dependencies, program.events...)
		if command.binding.Relation != nil {
			b.Dependencies = append(b.Dependencies, catalog.ExpressionDependency{Ref: command.binding.Ref, Revision: command.binding.Relation.Revision()})
		}
		b.Dependencies = append(b.Dependencies, command.binding.Dependencies...)
		b.Prepared.SequenceConstants = append(b.Prepared.SequenceConstants, command.binding.Prepared.SequenceConstants...)
		b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, command.binding.Prepared.RegtypeConstants...)
	}
	return nil
}

// Admit normalized actual targets before canonical CTE static preflight. The
// original parsed contexts remain present for caller/name/storage fences.
// Enter held; success retains Engine.mu, failure releases it.
func (e *Engine) admitRuleCTEsLocked(scope *StatementScope, statement parser.Statement, b *binding) ([]ids.ObjectRef, error) {
	if len(ruleCTEsOf(b.Rule.CTEs)) == 0 {
		return nil, nil
	}
	// Prepared bindings retain analysis recipes, never an execution's admitted
	// index or refreshed principal. Each request owns these mutable tuples.
	commands := make(map[*plan.CTEDefinition]*ruleCTECommand, len(ruleCTEsOf(b.Rule.CTEs)))
	for definition, recipe := range ruleCTEsOf(b.Rule.CTEs) {
		copy := *recipe
		commands[definition] = &copy
	}
	b.Rule.CTEs = ruleCTEsSlotOf(commands)
	program := ruleProgram{}
	var normalized []*ruleCTECommand
	for _, definition := range b.CTEs {
		rewrite := ruleCTEsOf(b.Rule.CTEs)[definition]
		if rewrite == nil {
			continue
		}
		mutation, _ := mutationBinding(definition)
		if err := rewrite.caller.checkBinding(*mutation, definition.Statement); err != nil {
			e.mu.Unlock()
			return nil, err
		}
		if _, selectOnly := rewrite.command.syntax.(*parser.Select); selectOnly {
			continue
		}
		program.commands = append(program.commands, rewrite.command)
		program.events = append(program.events, rewrite.events...)
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
	for _, rewrite := range ruleCTEsOf(b.Rule.CTEs) {
		caller := fresh
		caller.State.CurrentUser = rewrite.caller.State.CurrentUser
		if err := caller.Roles.ValidatePrincipal(caller.State.CurrentUser); err != nil {
			e.mu.Unlock()
			return nil, err
		}
		mutation, _ := mutationBinding(rewrite.definition)
		if err := checkRowSecurityBinding(scope, caller, *mutation); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	for _, rewrite := range ruleCTEsOf(b.Rule.CTEs) {
		caller := fresh
		caller.State.CurrentUser = rewrite.caller.State.CurrentUser
		mutation, _ := mutationBinding(rewrite.definition)
		if err := caller.checkBinding(*mutation, rewrite.definition.Statement); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	if len(scope.mutations) == 0 {
		scope.mutations = []statementMutation{scope.statementMutation}
		scope.activeMutation = 0
	}
	for i, rewrite := range normalized {
		rewrite.command = program.commands[i]
		if index := scope.mutationIndex(rewrite.definition.Statement); rewrite.command.syntax == rewrite.definition.Statement && index >= 0 {
			rewrite.mutation = index
			continue
		}
		physical := physicalBinding(rewrite.command.binding)
		scope.mutations[scope.activeMutation] = scope.statementMutation
		rewrite.mutation = len(scope.mutations)
		scope.mutations = append(scope.mutations, statementMutation{statement: rewrite.command.syntax, lockedRef: physical.Ref, lockedDescriptor: physical.Relation, lockedStorage: physical.Storage, viewWrite: viewWriteOf(rewrite.command.binding.ViewWrite)})
	}
	return refs, nil
}
