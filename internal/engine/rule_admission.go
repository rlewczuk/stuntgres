package engine

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Enter with Engine.mu held. Success retains it; failure releases it. This
// acquires the original ticket's locks, not data views or command effects.
func (e *Engine) admitRuleCommandsLocked(scope *StatementScope, statement parser.Statement, program *ruleProgram) ([]ids.ObjectRef, error) {
	commands, events := program.commands, program.events
	var seen map[*cteDefinition]bool
	expanded := false
	for i := 0; i < len(commands); i++ {
		for _, definition := range commands[i].binding.ctes {
			rewrite := commands[i].binding.ruleCTEs[definition]
			if rewrite == nil || seen[definition] {
				continue
			}
			if seen == nil {
				seen = make(map[*cteDefinition]bool)
			}
			seen[definition] = true
			if err := rewrite.caller.checkBinding(*definition.mutation, definition.statement); err != nil {
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
		}{event.ref, lock.AccessShare})
	}
	for _, command := range commands {
		physical := command.binding.physicalBinding()
		if physical.relation != nil && physical.relation.relationKind != viewRelation {
			requests = append(requests, struct {
				ref  ids.ObjectRef
				mode lock.Mode
			}{physical.ref, lock.RowExclusive})
			if !slices.Contains(refs, physical.ref) {
				refs = append(refs, physical.ref)
			}
		}
		collectAuth := command.auth
		collectAuth.recaptureCatalog(scope.inheritanceRead(command.auth.catalog))
		principal := func(leaf queryLeaf) authorization.PrincipalRef {
			if leaf.principal != (authorization.PrincipalRef{}) {
				return leaf.principal
			}
			return command.auth.state.CurrentUser
		}
		var queryRefs []ids.ObjectRef
		for start := 0; start < len(command.binding.querySources); {
			invoker := principal(command.binding.querySources[start])
			end := start + 1
			for end < len(command.binding.querySources) && principal(command.binding.querySources[end]) == invoker {
				end++
			}
			collectAuth.state.CurrentUser = invoker
			var err error
			queryRefs, err = e.queryTableRefsLocked(collectAuth, invoker, command.binding.querySources[start:end], queryRefs, scope.op.session.token, queryRowMark{})
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
		current, ok := fresh.catalog.Find(event.ref.Address)
		if !ok || event.ref.Epoch != fresh.catalog.Epoch() || current.Revision() != event.revision {
			e.mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
	}
	for i := range commands {
		principal := commands[i].auth.state.CurrentUser
		current := fresh
		current.state.CurrentUser = principal
		if i < len(program.commands) {
			program.commands[i].auth = current
		}
		if err := current.roles.ValidatePrincipal(principal); err != nil {
			e.mu.Unlock()
			return nil, err
		}
		if err := current.checkBinding(commands[i].binding, commands[i].syntax); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	return refs, nil
}
