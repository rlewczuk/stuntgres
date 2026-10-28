package core

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

// A command-private occurrence tree, not a permission/identity cache. Metadata
// admission completes the whole tree before a visibility root is captured.
// Materialization consumes these exact programs and stored roots once.
type querySourcePreparation struct {
	leaves      []exec.Leaf
	refs        []ids.ObjectRef
	singleton   bool
	invoker     authorization.PrincipalRef
	inherited   plan.RowMark
	single      [1]querySourceOccurrence
	occurrences []querySourceOccurrence
	consumed    bool
}
type querySourceOccurrence struct {
	program *exec.RLSProgram
	view    *storedSourcePreparation
}
type storedSourcePreparation struct {
	root       plan.Node
	definition *stored.View
	consumer   *catalog.Relation
	sources    *querySourcePreparation
}
type policySourcePreparation struct {
	clause  *exec.RLSRuntimeClause
	sources *querySourcePreparation
}

func (e *Engine) prepareQuerySourcesLocked(s *StatementScope, leaves []exec.Leaf, singleton bool, invoker authorization.PrincipalRef, inherited plan.RowMark) (*querySourcePreparation, error) {
	if err := s.executionContext().Err(); err != nil {
		return nil, err
	}
	if s.authority == nil {
		s.authority = &exec.Authority{Host: HostOf(s)}
	}
	if err := e.CaptureCommentSnapshotLocked(s); err != nil {
		return nil, err
	}
	p := &querySourcePreparation{leaves: leaves, singleton: singleton, invoker: invoker, inherited: inherited}
	if len(leaves) == 1 {
		p.occurrences = p.single[:]
	} else if len(leaves) > 1 {
		p.occurrences = make([]querySourceOccurrence, len(leaves))
	}
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return nil, err
	}
	auth.Recapture(partition.CaptureCatalog(s.inheritanceRead(e.PrivateCatalogLocked(s.PendingCatalog())), s.PartitionRelations, s.PendingDetachSnapshot()))
	p.refs, err = e.QueryTableRefsLocked(auth, invoker, leaves, nil, s.Op.Session.SessionToken, inherited)
	if err != nil {
		return nil, err
	}
	s.rlsDataRefs = append(s.rlsDataRefs, p.refs...)
	for i, leaf := range leaves {
		if leaf.AnalysisOnly {
			continue
		}
		mark := exec.EffectiveQueryRowMark(leaf.Relation, exec.MergeQueryRowMark(inherited, leaf.Mark))
		command := catalog.PolicySelect
		if mark.Mode != 0 {
			command = catalog.PolicyUpdate
		}
		p.occurrences[i].program, err = e.captureRowSecurityLocked(s, leaf.Relation, leaf.Principal, command, false, mark.Mode != 0)
		if err != nil {
			return nil, err
		}
		if p.occurrences[i].program.Denied() || leaf.Relation.RelationKind != catalog.ViewRelation {
			continue
		}
		for _, active := range s.ViewCreationOrZero().Active {
			if active == leaf.Relation.Address() {
				return nil, SQLError("42P17", fmt.Sprintf(`infinite recursion detected in rules for relation "%s"`, leaf.Relation.Name()))
			}
		}
		active := s.viewCreation()
		active.Active = append(active.Active, leaf.Relation.Address())
		uses := leaf.Uses
		if uses == nil {
			uses = []int{}
		}
		sourceInvoker := invoker
		if s.ruleAction.Auth != nil && leaf.Principal != (authorization.PrincipalRef{}) {
			sourceInvoker = leaf.Principal
		}
		p.occurrences[i].view, err = e.prepareStoredSourceLocked(s, leaf.Ref, stored.ViewOf(leaf.Relation), sourceInvoker, mark, uses)
		active.Active = active.Active[:len(active.Active)-1]
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *querySourcePreparation) volatile() bool {
	for i, leaf := range p.leaves {
		occurrence := &p.occurrences[i]
		if leaf.AnalysisOnly {
			continue
		}
		if leaf.Relation.RelationKind == catalog.ForeignRelation || occurrence.program != nil && occurrence.program.Volatile || occurrence.view != nil && occurrence.view.sources.volatile() {
			return true
		}
	}
	return false
}

// configure already owns the command's admission depth, so OLD/NEW and SQL
// source closure share one final metadata fence and one physical ref union.
func (e *Engine) prepareCommandSourcesLocked(s *StatementScope, bRef ids.ObjectRef) error {
	command := s.rlsCommand
	if command == nil {
		return nil
	}
	binding := view.PhysicalBinding(command.binding)
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	auth.State.CurrentUser = command.auth.State.CurrentUser
	auth.Recapture(s.inheritanceRead(e.PrivateCatalogLocked(s.PendingCatalog())))
	if binding.Relation != nil && binding.Relation.RelationKind.Stored() {
		s.rlsDataRefs, err = e.QueryTableRefsLocked(auth, auth.State.CurrentUser, nil, append(s.rlsDataRefs, s.Command.TargetRefs(bRef)...), s.Op.Session.SessionToken, plan.RowMark{})
		if err != nil {
			return err
		}
	}
	if binding.Relation != nil && binding.Relation.RelationKind == catalog.PartitionedRelation {
		target := [1]exec.Leaf{{Ref: binding.Ref, Relation: binding.Relation}}
		s.rlsDataRefs, err = e.QueryTableRefsLocked(auth, auth.State.CurrentUser, target[:], s.rlsDataRefs, s.Op.Session.SessionToken, plan.RowMark{})
		if err != nil {
			return err
		}
	}
	if len(binding.QuerySources) == 0 && !binding.SubqueryDemand && binding.Query == nil {
		return nil
	}
	command.sources, err = e.prepareQuerySourcesLocked(s, binding.QuerySources, false, auth.State.CurrentUser, plan.RowMark{})
	return err
}

// Called only after the complete selected closure has been admitted. Ordinary
// captureDataViews keeps its strict outside-view guard; no roots are rebased.
func (e *Engine) materializePolicySourcesLocked(s *StatementScope) error {
	if err := e.fenceRowSecurityProgramsLocked(s); err != nil {
		return err
	}
	if len(s.rlsDataRefs) != 0 && s.View == nil {
		if _, err := s.captureDataRootsAtLocked(e, s.rlsDataRefs, false, nil, false); err != nil {
			return err
		}
	}
	for _, program := range s.rlsPrograms {
		for i := range program.sources {
			pending := &program.sources[i]
			if pending.sources == nil {
				continue
			}
			inputs, err := e.capturePreparedQuerySourcesLocked(s, pending.sources, s.QueryLockContext)
			if err != nil {
				return err
			}
			pending.clause.State = exec.State{Source: inputs, SourceMode: exec.SourceReusable, Input: s.Input, Output: s.Output, Locking: inputs.Locks.RowMarked()}
			pending.clause.State.CapturePlannerAdmission()
			e.Mu.Unlock()
			err = exec.SelectQuerySources(s.executionContext(), inputs)
			if err == nil {
				pending.clause.State.Source = view.FreezeWriteSources(inputs, pending.sources.leaves, s.authority, HostOf(s))
			}
			e.Mu.Lock()
			if err != nil {
				return err
			}
			pending.sources = nil
		}
		program.sources = nil
	}
	return e.fenceRowSecurityProgramsLocked(s)
}
