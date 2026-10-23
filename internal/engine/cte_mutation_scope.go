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
type statementMutation struct {
	statement parser.Statement
	mutationTarget
	dml.Command
	effectAdmission foreignEffectAdmission
}

// mutationTarget is the admitted, locked command target. It stays in the
// engine: statement lock admission and DDL read it.
type mutationTarget struct {
	lockedRef             ids.ObjectRef
	lockedDescriptor      catalog.Descriptor
	lockedStorage         *storage.Relation
	lockedIdentityOnly    bool
	admittedTargetName    parser.ObjectName
	admittedTargetLexical ids.ObjectRef
	routineDependencies   []catalog.ExpressionDependency // Borrowed immutable manifest follows command activation.
}

func (s *StatementScope) activateMutation(index int) {
	if len(s.mutations) == 0 || index == s.activeMutation {
		return
	}
	s.mutations[s.activeMutation] = s.statementMutation
	s.statementMutation = s.mutations[index]
	s.activeMutation = index
}

func (s *StatementScope) mutationIndex(statement parser.Statement) int {
	for i := range s.mutations {
		if s.mutations[i].statement == statement {
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
		s.mutations = append(s.mutations, statementMutation{statement: command})
		return nil
	}); err != nil {
		return err
	}
	// Main SELECT/CTAS has an empty context; its admission must not reuse an
	// auxiliary's target. Main DML already occupies its final occurrence.
	root := dml.StatementCTERoot(statement)
	if !parser.ModifyingStatement(root) {
		s.mutations = append(s.mutations, statementMutation{statement: root})
	}
	s.activeMutation = 0
	s.statementMutation = s.mutations[0]
	for i := range s.mutations {
		s.activateMutation(i)
		command := s.statement
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
			if err := analyze.BindInsertTargets(s.lockedDescriptor.(*catalog.Relation), insert.Targets, nil); err != nil {
				return err
			}
		}
	}
	if err := s.acquireAdmittedQueryRelations(admission, tables, queries, statement, execute); err != nil {
		return err
	}
	for i := range s.mutations {
		mutation := s.mutations[i]
		if i == s.activeMutation {
			mutation = s.statementMutation
		}
		if mutation.lockedRef.Domain() == nil {
			continue
		}
		s.queryLockAdmission.MutationTargets = append(s.queryLockAdmission.MutationTargets, analyze.MutationTarget{Statement: mutation.statement, Ref: mutation.lockedRef})
	}
	if err := s.acquireViewSources(); err != nil {
		return err
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(s.op, statement)
	if err == nil {
		err = s.validateAdmissionNamesLocked(e, auth)
	}
	return err
}

// Retain the same view rewrite/grant path for auxiliary commands. Enter/return
// with Engine.mu held; a real target wait releases it, never changes the owner.
func (e *Engine) attachCTEViewTargetsLocked(scope *StatementScope, b *analyze.Binding, execute bool) error {
	if len(scope.mutations) == 0 {
		return nil
	}
	main := scope.activeMutation
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
			e.mu.Unlock()
			err := scope.acquireViewWriteTarget(view.ViewWriteOf(mutation.ViewWrite))
			e.mu.Lock()
			if err != nil {
				return err
			}
			mutation.Storage = scope.lockedStorage
		}
		if err := e.attachViewWriteSourcesLocked(scope, mutation); err != nil {
			return err
		}
	}
	auth, err := e.authorizationLocked(scope.op)
	if err == nil {
		err = scope.validateAdmissionNamesLocked(e, auth)
	}
	return err
}
