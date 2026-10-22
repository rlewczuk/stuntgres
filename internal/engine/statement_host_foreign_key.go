package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// statementHost implements dml.FinishHost: the FK runtime that Finish runs.
// The authorities it hands out are *AuthorizationView. The row, grant and
// admission bodies stay StatementScope methods because ALTER TABLE, CREATE
// TABLE and the write stage use them too.
var _ dml.FinishHost = (*statementHost)(nil)

func (h *statementHost) Staged() (map[ids.ObjectAddress][]storage.RowVersion, map[ids.ObjectAddress]*visibility.PrivateDeletes) {
	return h.stage.rows, h.stage.deletes
}

// foreignKeyChanges is the transaction catalog FK capture reads: the
// statement's own during DDL, the owner's otherwise.
func (s *StatementScope) foreignKeyChanges() *session.Journal {
	if s.catalog != nil {
		return s.catalog
	}
	return s.owner.Catalog
}

func (h *statementHost) ForeignKeyJobs() ([]dml.ForeignKeyJob, map[ids.ObjectRef]lock.Mode, dml.Authority, error) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return nil, nil, nil, err
	}
	auth.Catalog = e.privateCatalogLocked(s.foreignKeyChanges())
	auth.Recapture(auth.Catalog)
	jobs, modes, err := dml.ForeignKeyReach(auth.Catalog, s.stage.rows, s.stage.deletes)
	return jobs, modes, &auth, err
}

func (h *statementHost) RecaptureAuthority(a dml.Authority) dml.Authority {
	s := h.scope()
	e := s.engine()
	auth := *foreignAuthorizationOf(a)
	e.mu.Lock()
	auth.Catalog = e.privateCatalogLocked(s.foreignKeyChanges())
	auth.Recapture(auth.Catalog)
	e.mu.Unlock()
	return &auth
}

func (h *statementHost) ForeignKeyAuthority() (dml.Authority, error) {
	s := h.scope()
	auth, err := operationAuthorization(s.op)
	if err != nil {
		return nil, err
	}
	e := s.engine()
	e.mu.Lock()
	auth.Catalog = e.privateCatalogLocked(s.foreignKeyChanges())
	auth.Recapture(auth.Catalog)
	e.mu.Unlock()
	return &auth, nil
}

func (h *statementHost) ForeignKeyRows(ref ids.ObjectRef, idx ids.ObjectAddress) (table.ForeignKeyRows, error) {
	return h.scope().captureForeignKeyRows(ref, idx)
}

// RetainedForeignKeyRows borrows both images under stable isolation: current
// matches discover new and moved-in children, retained matches preserve
// moved-away successor conflicts. Neither creates another snapshot or copies
// values.
func (h *statementHost) RetainedForeignKeyRows(ref ids.ObjectRef, current table.ForeignKeyRows) (table.ForeignKeyRows, error) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	var retained table.ForeignKeyRows
	if s.owner.Isolation.Stable() && s.owner.SnapshotSet {
		if root, exists := s.owner.Roots[ref.Address]; exists {
			if root.Storage != current.Storage {
				return table.ForeignKeyRows{}, ids.ErrCatalogReplaced
			}
			retained = current
			retained.Committed, retained.Ends, retained.Frontier = root.Versions, root.Ends, s.owner.Frontier
		}
	}
	return retained, nil
}

func (h *statementHost) RefreshForeignKeyRows(ref ids.ObjectRef, rows *table.ForeignKeyRows) error {
	return h.scope().refreshForeignKeyRows(ref, rows)
}

func (h *statementHost) GrantForeignKey(ref ids.ObjectRef, row ids.RowID, mode lock.Mode, stored *storage.Relation) error {
	return h.scope().foreignKeyGrant(ref, row, mode, stored)
}

func (h *statementHost) RefreshForeignActionJob(job *dml.ForeignKeyJob) error {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	return dml.RefreshForeignActionJob(e.privateCatalogLocked(s.owner.Catalog), job)
}

func (h *statementHost) AdmitForeignAction(ref ids.ObjectRef, job *dml.ForeignKeyJob, writable bool) error {
	return h.scope().admitForeignAction(ref, job, writable)
}

func (h *statementHost) ForeignActionRow(ref ids.ObjectRef, candidate storage.RowVersion) (storage.RowVersion, bool, error) {
	return h.scope().foreignActionRow(ref, candidate)
}

func (h *statementHost) DeleteForeignAction(ref ids.ObjectRef, old storage.RowVersion) error {
	return h.stage.deleteForeignAction(ref, old)
}

// PlanForeignActionInput reads the statement's input environment, captures
// the type diagnostic names under Engine.mu when the targets carry domain
// checks, and for SET DEFAULT binds the child's defaults with the root binder
// and admits them. It works on a copy of a, as the plan always has.
func (h *statementHost) PlanForeignActionInput(a dml.Authority, job *dml.ForeignKeyJob, plan *dml.ForeignActionPlan, action parser.ForeignKeyAction) error {
	s := h.scope()
	e := s.engine()
	auth := *foreignAuthorizationOf(a)
	plan.Input = s.input
	if plan.DomainChecks {
		e.mu.Lock()
		name, err := e.captureTypeDiagnosticName(auth)
		e.mu.Unlock()
		if err != nil {
			return err
		}
		plan.Input.TypeDiagnosticName = name
	}
	if action != parser.ForeignKeySetDefault {
		return nil
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: plan.Input, Output: s.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	b := analyze.Binding{Relation: job.Child}
	analyze.BindInsertDefaults(e.catalog.owner(), &b, auth.Catalog, parser.OverrideNone)
	plan.Defaults = make([]expr.Expr, len(plan.Targets))
	plan.DefaultOrigins = make([]analyze.DefaultOrigin, len(plan.Targets))
	for i, slot := range plan.Targets {
		if len(b.Insert.Defaults) != 0 {
			plan.Defaults[i] = b.Insert.Defaults[slot]
			plan.DefaultOrigins[i] = view.BindingDefaultOrigin(&b, slot)
		}
	}
	// Retained defaults can themselves cast to a checked domain even when
	// the action destination is an ordinary base type.
	binder := analyze.Binder{Host: e.analysisHost(s.op), Ctx: s.op.Context(), Input: plan.Input, Query: analyze.QueryScopeState{Auth: &auth}}
	e.mu.Lock()
	err := binder.CaptureRetainedWriteInput(plan.Defaults, nil, nil)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	plan.Input, evaluation.Input = binder.Input, binder.Input
	if plan.DomainChecks {
		evaluation.Input.DomainChecks = &evaluation
	}
	for i, expression := range plan.Defaults {
		if expression == nil {
			continue
		}
		defaultEvaluation := evaluation
		analyze.ApplyDefaultOrigin(view.BindingDefaultOrigin(&b, plan.Targets[i]), &defaultEvaluation)
		folded, _, err := exec.PreflightCoercionsWithPolicy(&defaultEvaluation, expression, true)
		if err != nil {
			return err
		}
		if err := exec.AdmitExpression(&defaultEvaluation, folded, auth.Catalog); err != nil {
			return err
		}
		plan.Defaults[i] = folded
	}
	return nil
}

func (h *statementHost) SwapForeignActionAuthority(next dml.Authority) dml.Authority {
	previous := h.foreignActionAuth
	h.foreignActionAuth = nil
	if next != nil {
		h.foreignActionAuth = foreignAuthorizationOf(next)
	}
	if previous == nil {
		return nil
	}
	return previous
}
