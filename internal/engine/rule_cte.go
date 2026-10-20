package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// The original definition remains the provider key and SQL/output authority.
// Only this execution-private tuple chooses the executable command/context.
type ruleCTECommand struct {
	definition *cteDefinition
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
	needed := b.rule.template || matched(bindingRuleRelation(*b), statement)
	for _, definition := range b.ctes {
		if mutation, ok := mutationBinding(definition); ok && matched(bindingRuleRelation(*mutation), definition.statement) {
			needed = true
			break
		}
	}
	if !needed {
		return nil
	}
	for _, definition := range b.ctes {
		mutation, ok := mutationBinding(definition)
		if !ok || b.rule.ctes[definition] != nil {
			continue
		}
		original := *mutation
		original.parameters = b.parameters
		original.ctes = b.ctes
		program := ruleProgram{count: -1, returning: -1}
		if _, err := e.expandRulesLocked(scope, auth, definition.statement, original, nil, &program); err != nil {
			return err
		}
		if err := ruleCTEProgramError(program); err != nil {
			return err
		}
		command := program.commands[0]
		if b.rule.ctes == nil {
			b.rule.ctes = make(map[*cteDefinition]*ruleCTECommand)
		}
		b.rule.ctes[definition] = &ruleCTECommand{definition: definition, command: command, caller: auth, events: program.events, mutation: -1}
		b.dependencies = append(b.dependencies, program.events...)
		if command.binding.relation != nil {
			b.dependencies = append(b.dependencies, catalog.ExpressionDependency{Ref: command.binding.ref, Revision: command.binding.relation.Revision()})
		}
		b.dependencies = append(b.dependencies, command.binding.dependencies...)
		b.prepared.sequenceConstants = append(b.prepared.sequenceConstants, command.binding.prepared.sequenceConstants...)
		b.prepared.regtypeConstants = append(b.prepared.regtypeConstants, command.binding.prepared.regtypeConstants...)
	}
	return nil
}

// Admit normalized actual targets before canonical CTE static preflight. The
// original parsed contexts remain present for caller/name/storage fences.
// Enter held; success retains Engine.mu, failure releases it.
func (e *Engine) admitRuleCTEsLocked(scope *StatementScope, statement parser.Statement, b *binding) ([]ids.ObjectRef, error) {
	if len(b.rule.ctes) == 0 {
		return nil, nil
	}
	// Prepared bindings retain analysis recipes, never an execution's admitted
	// index or refreshed principal. Each request owns these mutable tuples.
	commands := make(map[*cteDefinition]*ruleCTECommand, len(b.rule.ctes))
	for definition, recipe := range b.rule.ctes {
		copy := *recipe
		commands[definition] = &copy
	}
	b.rule.ctes = commands
	program := ruleProgram{}
	var normalized []*ruleCTECommand
	for _, definition := range b.ctes {
		rewrite := b.rule.ctes[definition]
		if rewrite == nil {
			continue
		}
		mutation, _ := mutationBinding(definition)
		if err := rewrite.caller.checkBinding(*mutation, definition.statement); err != nil {
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
	for _, rewrite := range b.rule.ctes {
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
	for _, rewrite := range b.rule.ctes {
		caller := fresh
		caller.State.CurrentUser = rewrite.caller.State.CurrentUser
		mutation, _ := mutationBinding(rewrite.definition)
		if err := caller.checkBinding(*mutation, rewrite.definition.statement); err != nil {
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
		if index := scope.mutationIndex(rewrite.definition.statement); rewrite.command.syntax == rewrite.definition.statement && index >= 0 {
			rewrite.mutation = index
			continue
		}
		physical := rewrite.command.binding.physicalBinding()
		scope.mutations[scope.activeMutation] = scope.statementMutation
		rewrite.mutation = len(scope.mutations)
		scope.mutations = append(scope.mutations, statementMutation{statement: rewrite.command.syntax, lockedRef: physical.ref, lockedDescriptor: physical.relation, lockedStorage: physical.storage, viewWrite: rewrite.command.binding.viewWrite})
	}
	return refs, nil
}
