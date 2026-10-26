package core

import (
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

// The bound main/modifier bundle has passed AdmitRuleCommandsLocked's real
// grants and readmission. Install its exact statement occurrences, then admit
// bound view/hierarchy targets through their incumbent owners. Enter held;
// success retains Engine.Mu, failure releases it. No target is resolved by name.
func (s *StatementScope) admitSQLRoutineCTETargetsLocked(program *view.RuleProgram, refs []ids.ObjectRef) ([]ids.ObjectRef, error) {
	e := s.Engine()
	s.mutations = make([]StatementMutation, len(program.Commands))
	for i, command := range program.Commands {
		physical := view.PhysicalBinding(command.Binding)
		s.mutations[i] = StatementMutation{Statement: command.Syntax, MutationTarget: MutationTarget{
			LockedRef: physical.Ref, LockedDescriptor: physical.Relation, LockedStorage: physical.Storage,
		}}
		s.mutations[i].ViewWrite = view.ViewWriteOf(command.Binding.ViewWrite)
	}
	main := program.Commands[0]
	physical := view.PhysicalBinding(main.Binding)
	s.ActiveMutation = 0
	s.installRuleScratch(ruleCommandScratch{Mutation: s.mutations[0], Ref: physical.Ref, Descriptor: physical.Relation, Storage: physical.Storage, ViewWrite: view.ViewWriteOf(main.Binding.ViewWrite), Sources: main.Binding.QuerySources})
	defer s.activateMutation(0)
	e.Mu.Unlock()
	for i, command := range program.Commands {
		s.activateMutation(i)
		if s.ViewWrite != nil {
			if err := s.acquireViewWriteTarget(s.ViewWrite); err != nil {
				return nil, err
			}
		}
		if err := s.acquireInheritedMutationTargets(command.Syntax); err != nil {
			return nil, err
		}
		for _, target := range s.InheritedTargets {
			if target.Relation.Partition == nil && !slices.Contains(refs, target.Ref) {
				refs = append(refs, target.Ref)
			}
		}
	}
	s.activateMutation(0)
	e.Mu.Lock()
	if err := s.RefreshRelationGrantsLocked(e); err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	if err := s.Op.Context().Err(); err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	fresh, err := e.StatementAuthorizationLocked(s.Op, main.Syntax)
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	for i := range program.Commands {
		command := &program.Commands[i]
		current := fresh
		current.State.CurrentUser = command.Auth.Access().State.CurrentUser
		if err := current.Roles.ValidatePrincipal(current.State.CurrentUser); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		if err := view.CheckRowSecurityBinding(s.RowSecurity, current.View, command.Binding); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		if err := analyze.CheckBinding(current, command.Binding, command.Syntax); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		command.Auth = current
	}
	return refs, nil
}
