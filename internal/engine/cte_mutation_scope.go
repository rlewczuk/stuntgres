package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// Command-local state is switched inside one statement owner. Stage, view,
// operation, ticket, constraint queues and publication never move with it.
// The embedded context keeps single-command statements allocation-free.
// The DML groups are dml.Command; every group is a value, so activateMutation
// and the rule and SQL routine save/restore copy the whole mutation at once.
type StatementMutation struct {
	Statement parser.Statement
	MutationTarget
	dml.Command
	effectAdmission foreignEffectAdmission
}

// MutationTarget is the admitted, locked command target. It stays in the
// engine: statement lock admission and DDL read it.
type MutationTarget struct {
	LockedRef             ids.ObjectRef
	LockedDescriptor      catalog.Descriptor
	LockedStorage         *storage.Relation
	lockedIdentityOnly    bool
	AdmittedTargetName    parser.ObjectName
	AdmittedTargetLexical ids.ObjectRef
	RoutineDependencies   []catalog.ExpressionDependency // Borrowed immutable manifest follows command activation.
}

func (s *StatementScope) activateMutation(index int) {
	if len(s.mutations) == 0 || index == s.ActiveMutation {
		return
	}
	s.mutations[s.ActiveMutation] = s.StatementMutation
	s.StatementMutation = s.mutations[index]
	s.ActiveMutation = index
}

func (s *StatementScope) mutationIndex(statement parser.Statement) int {
	for i := range s.mutations {
		if s.mutations[i].Statement == statement {
			return i
		}
	}
	return -1
}

// Admission is complete before binding/execution. The existing source walker
// remains lexical authority; target occurrences cannot become virtual scans.
func (s *StatementScope) acquireCTEMutations(statement parser.Statement, execute bool) error {
	// Validate duplicate/nesting restrictions before resolving any body target.
	admission, tables, queries, err := analyze.NewLockAdmission(statement)
	if err != nil {
		return err
	}
	if err := parser.VisitMutationTargets(statement, func(command parser.Statement) error {
		s.mutations = append(s.mutations, StatementMutation{Statement: command})
		return nil
	}); err != nil {
		return err
	}
	// Main SELECT/CTAS has an empty context; its admission must not reuse an
	// auxiliary's target. Main DML already occupies its final occurrence.
	root := dml.StatementCTERoot(statement)
	if !parser.ModifyingStatement(root) {
		s.mutations = append(s.mutations, StatementMutation{Statement: root})
	}
	s.ActiveMutation = 0
	s.StatementMutation = s.mutations[0]
	for i := range s.mutations {
		s.activateMutation(i)
		command := s.Statement
		if !parser.ModifyingStatement(command) {
			continue
		}
		if merge, ok := command.(*parser.Merge); ok {
			if err := analyze.ValidateMergeReachability(merge); err != nil {
				return err
			}
		}
		if err := s.acquireRelationTarget(command, statement, execute, false); err != nil {
			return err
		}
		if err := s.acquireInheritedMutationTargets(command); err != nil {
			return err
		}
		if insert, ok := command.(*parser.Insert); ok {
			if err := analyze.BindInsertTargets(s.LockedDescriptor.(*catalog.Relation), insert.Targets, nil); err != nil {
				return err
			}
		}
	}
	if err := s.acquireAdmittedQueryRelations(admission, tables, queries, statement, execute); err != nil {
		return err
	}
	for i := range s.mutations {
		mutation := s.mutations[i]
		if i == s.ActiveMutation {
			mutation = s.StatementMutation
		}
		if mutation.LockedRef.Domain() == nil {
			continue
		}
		s.QueryLockAdmission.MutationTargets = append(s.QueryLockAdmission.MutationTargets, analyze.MutationTarget{Statement: mutation.Statement, Ref: mutation.LockedRef})
	}
	if _, err := s.AcquireViewSources(false); err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.StatementAuthorizationLocked(s.Op, statement)
	if err == nil {
		err = s.ValidateAdmissionNamesLocked(e, auth)
	}
	return err
}

// Retain the same view rewrite/grant path for auxiliary commands. Enter/return
// with Engine.mu held; a real target wait releases it, never changes the owner.
func (e *Engine) attachCTEViewTargetsLocked(scope *StatementScope, b *analyze.Binding, execute bool) error {
	if len(scope.mutations) == 0 {
		return nil
	}
	main := scope.ActiveMutation
	defer scope.activateMutation(main)
	for _, definition := range b.CTEs {
		mutation, ok := analyze.MutationBinding(definition)
		if !ok || mutation.ViewWrite == nil {
			continue
		}
		index := scope.mutationIndex(definition.Statement)
		if index < 0 {
			return ids.ErrCatalogReplaced
		}
		scope.activateMutation(index)
		if execute {
			e.Mu.Unlock()
			err := scope.acquireViewWriteTarget(view.ViewWriteOf(mutation.ViewWrite))
			e.Mu.Lock()
			if err != nil {
				return err
			}
			mutation.Storage = scope.LockedStorage
		}
		if err := e.attachViewWriteSourcesLocked(scope, mutation); err != nil {
			return err
		}
	}
	auth, err := e.AuthorizationLocked(scope.Op)
	if err == nil {
		err = scope.ValidateAdmissionNamesLocked(e, auth)
	}
	return err
}
