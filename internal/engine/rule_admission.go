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
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// Enter with Engine.mu held. Success retains it; failure releases it. This
// acquires the original ticket's locks, not data views or command effects.
func (e *Engine) AdmitRuleCommandsLocked(scope *StatementScope, statement parser.Statement, program *view.RuleProgram) ([]ids.ObjectRef, error) {
	commands, events := program.Commands, program.Events
	var seen map[*plan.CTEDefinition]bool
	expanded := false
	for i := 0; i < len(commands); i++ {
		for _, definition := range commands[i].Binding.CTEs {
			rewrite := view.RuleCTEsOf(commands[i].Binding.Rule.CTEs)[definition]
			if rewrite == nil || seen[definition] {
				continue
			}
			if seen == nil {
				seen = make(map[*plan.CTEDefinition]bool)
			}
			seen[definition] = true
			mutation, _ := analyze.MutationBinding(definition)
			if err := analyze.CheckBinding(RuleAuthorizationOf(rewrite.Caller), *mutation, definition.Statement); err != nil {
				e.Mu.Unlock()
				return nil, err
			}
			if _, selectOnly := rewrite.Command.Syntax.(*parser.Select); selectOnly {
				continue
			}
			if !expanded {
				commands = slices.Clone(commands)
				events = slices.Clone(events)
				expanded = true
			}
			commands = append(commands, rewrite.Command)
			events = append(events, rewrite.Events...)
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
		physical := view.PhysicalBinding(command.Binding)
		if physical.Relation != nil && physical.Relation.RelationKind != catalog.ViewRelation {
			requests = append(requests, struct {
				ref  ids.ObjectRef
				mode lock.Mode
			}{physical.Ref, lock.RowExclusive})
			if !slices.Contains(refs, physical.Ref) {
				refs = append(refs, physical.Ref)
			}
		}
		commandAuth := RuleAuthorizationOf(command.Auth)
		collectAuth := commandAuth
		collectAuth.Recapture(scope.inheritanceRead(commandAuth.Catalog))
		principal := func(leaf exec.Leaf) authorization.PrincipalRef {
			if leaf.Principal != (authorization.PrincipalRef{}) {
				return leaf.Principal
			}
			return commandAuth.State.CurrentUser
		}
		var queryRefs []ids.ObjectRef
		for start := 0; start < len(command.Binding.QuerySources); {
			invoker := principal(command.Binding.QuerySources[start])
			end := start + 1
			for end < len(command.Binding.QuerySources) && principal(command.Binding.QuerySources[end]) == invoker {
				end++
			}
			collectAuth.State.CurrentUser = invoker
			var err error
			queryRefs, err = e.QueryTableRefsLocked(collectAuth, invoker, command.Binding.QuerySources[start:end], queryRefs, scope.Op.Session.SessionToken, plan.RowMark{})
			if err != nil {
				e.Mu.Unlock()
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
	e.Mu.Unlock()
	policy := lock.Policy{TimeoutSlot: &scope.Op.LockWait, Timeout: scope.Op.LockTimeout, StatementDeadline: scope.Op.StatementDeadline, Source: scope.Op.SourceContext}
	for _, requested := range requests {
		request, err := e.Locks.StartAcquire(scope.Op.Context(), scope.LockTicket, lock.Resource{ObjectRef: requested.ref}, requested.mode, policy)
		if request != nil {
			if scope.ReplacementRead {
				e.ReplacementMu.RUnlock()
				scope.ReplacementRead = false
			}
			err = request.Wait()
		}
		if err != nil {
			return nil, err
		}
		if scope.implicit && !scope.ReplacementRead {
			e.ReplacementMu.RLock()
			scope.ReplacementRead = true
		}
	}
	e.Mu.Lock()
	if err := scope.RefreshRelationGrantsLocked(e); err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	fresh, err := e.StatementAuthorizationLocked(scope.Op, statement)
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	for _, event := range events {
		current, ok := fresh.Catalog.Find(event.Ref.Address)
		if !ok || event.Ref.Epoch != fresh.Catalog.Epoch() || current.Revision() != event.Revision {
			e.Mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
	}
	for i := range commands {
		principal := commands[i].Auth.Access().State.CurrentUser
		current := fresh
		current.State.CurrentUser = principal
		if i < len(program.Commands) {
			program.Commands[i].Auth = current
		}
		if err := current.Roles.ValidatePrincipal(principal); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		commands[i].Auth = current
		if err := view.CheckRowSecurityBinding(scope.RowSecurity, current.View, commands[i].Binding); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
	}
	for i := range commands {
		current := RuleAuthorizationOf(commands[i].Auth)
		if err := analyze.CheckBinding(current, commands[i].Binding, commands[i].Syntax); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
	}
	return refs, nil
}
