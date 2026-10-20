package engine

import (
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Enter with Engine.mu held. Success retains it; failure releases it. This
// acquires the original ticket's locks, not data views or command effects.
func (e *Engine) admitRuleCommandsLocked(scope *StatementScope, statement parser.Statement, program *ruleProgram) ([]ids.ObjectRef, error) {
	commands, events := program.commands, program.events
	var seen map[*plan.CTEDefinition]bool
	expanded := false
	for i := 0; i < len(commands); i++ {
		for _, definition := range commands[i].binding.CTEs {
			rewrite := ruleCTEsOf(commands[i].binding.Rule.CTEs)[definition]
			if rewrite == nil || seen[definition] {
				continue
			}
			if seen == nil {
				seen = make(map[*plan.CTEDefinition]bool)
			}
			seen[definition] = true
			mutation, _ := analyze.MutationBinding(definition)
			if err := analyze.CheckBinding(rewrite.caller, *mutation, definition.Statement); err != nil {
				e.mu.Unlock()
				return nil, err
			}
			if _, selectOnly := rewrite.command.syntax.(*parser.Select); selectOnly {
				continue
			}
			if !expanded {
				commands = slices.Clone(commands)
				events = slices.Clone(events)
				expanded = true
			}
			commands = append(commands, rewrite.command)
			events = append(events, rewrite.events...)
		}
	}
	var refs []ids.ObjectRef
	var requests []struct {
		ref  ids.ObjectRef
		mode lock.Mode
	}
	for _, event := range events {
		requests = append(requests, struct {
			ref  ids.ObjectRef
			mode lock.Mode
		}{event.Ref, lock.AccessShare})
	}
	for _, command := range commands {
		physical := physicalBinding(command.binding)
		if physical.Relation != nil && physical.Relation.RelationKind != catalog.ViewRelation {
			requests = append(requests, struct {
				ref  ids.ObjectRef
				mode lock.Mode
			}{physical.Ref, lock.RowExclusive})
			if !slices.Contains(refs, physical.Ref) {
				refs = append(refs, physical.Ref)
			}
		}
		collectAuth := command.auth
		collectAuth.Recapture(scope.inheritanceRead(command.auth.Catalog))
		principal := func(leaf exec.Leaf) authorization.PrincipalRef {
			if leaf.Principal != (authorization.PrincipalRef{}) {
				return leaf.Principal
			}
			return command.auth.State.CurrentUser
		}
		var queryRefs []ids.ObjectRef
		for start := 0; start < len(command.binding.QuerySources); {
			invoker := principal(command.binding.QuerySources[start])
			end := start + 1
			for end < len(command.binding.QuerySources) && principal(command.binding.QuerySources[end]) == invoker {
				end++
			}
			collectAuth.State.CurrentUser = invoker
			var err error
			queryRefs, err = e.queryTableRefsLocked(collectAuth, invoker, command.binding.QuerySources[start:end], queryRefs, scope.op.session.token, plan.RowMark{})
			if err != nil {
				e.mu.Unlock()
				return nil, err
			}
			start = end
		}
		for _, ref := range queryRefs {
			requests = append(requests, struct {
				ref  ids.ObjectRef
				mode lock.Mode
			}{ref, lock.AccessShare})
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	e.mu.Unlock()
	policy := lock.Policy{TimeoutSlot: &scope.op.lockWait, Timeout: scope.op.lockTimeout, StatementDeadline: scope.op.statementDeadline, Source: scope.op.sourceContext}
	for _, requested := range requests {
		request, err := e.locks.StartAcquire(scope.op.Context(), scope.ticket, lock.Resource{ObjectRef: requested.ref}, requested.mode, policy)
		if request != nil {
			if scope.replacementRead {
				e.replacementMu.RUnlock()
				scope.replacementRead = false
			}
			err = request.Wait()
		}
		if err != nil {
			return nil, err
		}
		if scope.implicit && !scope.replacementRead {
			e.replacementMu.RLock()
			scope.replacementRead = true
		}
	}
	e.mu.Lock()
	if err := scope.refreshRelationGrantsLocked(e); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	fresh, err := e.statementAuthorizationLocked(scope.op, statement)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	for _, event := range events {
		current, ok := fresh.Catalog.Find(event.Ref.Address)
		if !ok || event.Ref.Epoch != fresh.Catalog.Epoch() || current.Revision() != event.Revision {
			e.mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
	}
	for i := range commands {
		principal := commands[i].auth.State.CurrentUser
		current := fresh
		current.State.CurrentUser = principal
		if i < len(program.commands) {
			program.commands[i].auth = current
		}
		if err := current.Roles.ValidatePrincipal(principal); err != nil {
			e.mu.Unlock()
			return nil, err
		}
		commands[i].auth = current
		if err := checkRowSecurityBinding(scope, current, commands[i].binding); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	for i := range commands {
		current := commands[i].auth
		if err := analyze.CheckBinding(current, commands[i].binding, commands[i].syntax); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	return refs, nil
}
