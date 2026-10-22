package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) planForeignAction(auth AuthorizationView, job *dml.ForeignKeyJob, event dml.ForeignActionTransition, action parser.ForeignKeyAction) (*dml.ForeignActionPlan, error) {
	cached := job.DeletePlan
	if event.Next.Row != 0 {
		cached = job.UpdatePlan
	}
	if cached != nil {
		return cached, nil
	}
	attributes := job.Definition.Attributes
	if event.Next.Row == 0 && len(job.Definition.DeleteAttributes) != 0 {
		attributes = job.Definition.DeleteAttributes
	}
	plan := &dml.ForeignActionPlan{Targets: make([]int, len(attributes))}
	for i, a := range attributes {
		plan.Targets[i] = slices.Index(job.Child.Attributes, a)
		if plan.Targets[i] < 0 {
			return nil, ids.ErrCatalogReplaced
		}
	}
	plan.Input = s.input
	for _, slot := range plan.Targets {
		plan.DomainChecks = plan.DomainChecks || sqltype.ContainsDomainConstraints(job.Child.Columns[slot].Usage.Type)
	}
	if plan.DomainChecks {
		e := s.engine()
		e.mu.Lock()
		name, err := e.captureTypeDiagnosticName(auth)
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		plan.Input.TypeDiagnosticName = name
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: plan.Input, Output: s.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	if plan.DomainChecks {
		evaluation.Input.DomainChecks = &evaluation
	}
	if action == parser.ForeignKeySetDefault {
		b := analyze.Binding{Relation: job.Child}
		e := s.engine()
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
			return nil, err
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
				return nil, err
			}
			if err := exec.AdmitExpression(&defaultEvaluation, folded, auth.Catalog); err != nil {
				return nil, err
			}
			plan.Defaults[i] = folded
		}
	}
	if action == parser.ForeignKeySetNull && plan.DomainChecks {
		plan.Defaults = make([]expr.Expr, len(plan.Targets))
		for i, slot := range plan.Targets {
			if job.Child.Columns[slot].Usage.Type.HasDomainConstraints() {
				plan.Defaults[i] = analyze.DomainNullInputExpression(job.Child.Columns[slot].Usage)
			}
		}
	}
	if action == parser.ForeignKeyCascade {
		plan.ParentSlots = make([]int, len(attributes))
		plan.Conversions = make([]sqltype.CoercionPlan, len(attributes))
		plan.Convert = make([]bool, len(attributes))
		for i, a := range attributes {
			at := slices.Index(job.Definition.Attributes, a)
			parentSlot := slices.Index(job.Parent.Attributes, job.Definition.Referenced[at])
			if parentSlot < 0 {
				return nil, ids.ErrCatalogReplaced
			}
			plan.ParentSlots[i] = parentSlot
			source, dest := job.Parent.Columns[parentSlot].Usage, job.Child.Columns[plan.Targets[i]].Usage
			if source != dest {
				var err error
				plan.Conversions[i], err = sqltype.PlanCoercion(source, dest, sqltype.Assignment)
				if err != nil {
					return nil, err
				}
				plan.Convert[i] = true
			}
		}
	}
	if event.Next.Row == 0 {
		job.DeletePlan = plan
	} else {
		job.UpdatePlan = plan
	}
	return plan, nil
}

func (s *StatementScope) applyForeignAction(auth AuthorizationView, job *dml.ForeignKeyJob, event dml.ForeignActionTransition, action parser.ForeignKeyAction) error {
	e := s.engine()
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), job.Child.Address())
	if err := s.admitForeignAction(ref, job, action != parser.ForeignKeyRestrict); err != nil {
		return err
	}
	reportAuth := foreignActionOwner(auth, event.Owner)
	if action == parser.ForeignKeyRestrict {
		children, err := s.captureForeignKeyRows(ref, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		return children.Each(func(child storage.RowVersion) (bool, error) {
			match, err := table.ForeignKeyRowsMatch(s.op.Context(), job.Comparisons, child.Values, event.Old.Values, s.input)
			if err != nil || !match {
				return false, err
			}
			if err = s.foreignKeyGrant(ref, child.Row, lock.ForKeyShare, children.Storage); err != nil {
				return false, err
			}
			latest, live, err := s.foreignActionRow(ref, child)
			if err != nil || !live {
				return false, err
			}
			if err := s.physicalRowEvent(ref, latest, lock.MultiKeyShare, 0); err != nil {
				return false, err
			}
			match, err = table.ForeignKeyRowsMatch(s.op.Context(), job.Comparisons, latest.Values, event.Old.Values, s.input)
			if err != nil || !match {
				return false, err
			}
			d := table.ForeignKeyDiagnostic(s.op.Context(), reportAuth.View, reportAuth, job.Child, job.Parent, job.Definition, event.Old.Values, true, false, s.output)
			var diagnostic *sqlstate.Error
			if !errors.As(d, &diagnostic) {
				return false, d
			}
			copy := *diagnostic
			copy.Code = "23001"
			copy.Message = fmt.Sprintf(`update or delete on table "%s" violates RESTRICT setting of foreign key constraint "%s" on table "%s"`, job.Parent.Name(), job.Definition.Name, job.Child.Name())
			copy.Detail = strings.Replace(copy.Detail, " is still referenced", " is referenced", 1)
			return false, &copy
		})
	}
	ownerAuth := foreignActionOwner(auth, job.Child.Owner)
	previous := s.foreignActionAuth
	s.foreignActionAuth = &ownerAuth
	defer func() { s.foreignActionAuth = previous }()
	deletion := action == parser.ForeignKeyCascade && event.Next.Row == 0
	var plan *dml.ForeignActionPlan
	var err error
	if !deletion {
		plan, err = s.planForeignAction(ownerAuth, job, event, action)
		if err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
	}
	children, retained, err := s.captureForeignActionRows(ref)
	if err != nil {
		return err
	}
	// Retained matches take precedence by real RowID. The canonical grant and
	// live-successor crosscheck below run before any latest-key match/staging.
	var candidates []storage.RowVersion
	var retainedMatches map[ids.RowID]struct{}
	if retained.Storage != nil {
		err = retained.Each(func(child storage.RowVersion) (bool, error) {
			match, err := table.ForeignKeyRowsMatch(s.op.Context(), job.Comparisons, child.Values, event.Old.Values, s.input)
			if err != nil || !match {
				return false, err
			}
			if retainedMatches == nil {
				retainedMatches = make(map[ids.RowID]struct{})
			}
			retainedMatches[child.Row] = struct{}{}
			candidates = append(candidates, child)
			return false, nil
		})
		if err != nil {
			return err
		}
	}
	err = children.Each(func(child storage.RowVersion) (bool, error) {
		if _, exists := retainedMatches[child.Row]; exists {
			return false, nil
		}
		match, err := table.ForeignKeyRowsMatch(s.op.Context(), job.Comparisons, child.Values, event.Old.Values, s.input)
		if match {
			candidates = append(candidates, child)
		}
		return false, err
	})
	if err != nil {
		return err
	}
	input := s.input
	if plan != nil {
		input = plan.Input
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: ownerAuth.Catalog, Input: input, Output: s.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &ownerAuth}}
	if plan != nil && plan.DomainChecks {
		evaluation.Input.DomainChecks = &evaluation
	}
	schema := auth.Catalog.Get(job.Child.Namespace()).Name()
	for _, candidate := range candidates {
		mode := lock.ForNoKeyUpdate
		if deletion {
			mode = lock.ForUpdate
		}
		if err = s.foreignKeyGrant(ref, candidate.Row, mode, children.Storage); err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
		e.mu.Lock()
		current := e.privateCatalogLocked(s.owner.Catalog)
		err = dml.RefreshForeignActionJob(current, job)
		e.mu.Unlock()
		if err != nil {
			return err
		}
		old, live, rowErr := s.foreignActionRow(ref, candidate)
		if rowErr != nil {
			return s.foreignActionError(rowErr, job, event, action, auth.Catalog)
		}
		if !live {
			continue
		}
		if old.Version != candidate.Version {
			lockMode, err := lock.MultiLockMode(mode)
			if err != nil {
				return err
			}
			if err := s.physicalRowEvent(ref, old, lockMode, 0); err != nil {
				return s.foreignActionError(err, job, event, action, auth.Catalog)
			}
		}
		match, err := table.ForeignKeyRowsMatch(s.op.Context(), job.Comparisons, old.Values, event.Old.Values, s.input)
		if err != nil {
			return err
		}
		if !match {
			continue
		}
		if deletion {
			if err = s.stage.deleteForeignAction(ref, old); err != nil {
				return s.foreignActionError(err, job, event, action, auth.Catalog)
			}
			continue
		}
		row := slices.Clone(old.Values)
		evaluation.Row = old.Values
		for i, slot := range plan.Targets {
			switch action {
			case parser.ForeignKeyCascade:
				row[slot] = event.Next.Values[plan.ParentSlots[i]]
				if plan.Convert[i] {
					row[slot], err = plan.Conversions[i].Apply(evaluation.Context, row[slot], evaluation.Input, evaluation.Output)
				}
			case parser.ForeignKeySetNull, parser.ForeignKeySetDefault:
				row[slot] = sqltype.Null(job.Child.Columns[slot].Usage.Type)
				if len(plan.Defaults) != 0 && plan.Defaults[i] != nil {
					if len(plan.DefaultOrigins) != 0 {
						row[slot], err = analyze.EvalDefault(&evaluation, plan.Defaults[i], plan.DefaultOrigins[i])
					} else {
						row[slot], err = plan.Defaults[i].Eval(&evaluation)
					}
				}
			}
			if err != nil {
				return s.foreignActionError(err, job, event, action, auth.Catalog)
			}
		}
		b := analyze.Binding{Ref: ref, Relation: job.Child, Targets: plan.Targets}
		if err = ValidateMutationRow(s.writeHost(), b, schema, row, &evaluation); err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
		keyChanged, err := dml.UniqueKeyChanged(s.writeHost(), ref, old.Values, row)
		if err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
		if keyChanged {
			if err = s.foreignKeyGrant(ref, old.Row, lock.ForUpdate, children.Storage); err != nil {
				return s.foreignActionError(err, job, event, action, auth.Catalog)
			}
		}
		if err = dml.ClaimUniqueRow(s.writeHost(), ref, old); err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
		old.PendingKeyChange = old.PendingKeyChange || keyChanged
		actionMode := lock.MultiNoKeyAction
		if keyChanged {
			actionMode = lock.MultiUpdateAction
		}
		if err := s.physicalRowEvent(ref, old, actionMode, 0); err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
		predecessor := [1]storage.RowVersion{old}
		proposal := index.WriteCandidate{Values: row}
		if err = dml.CheckUniqueRow(s.writeHost(), ref, &proposal, predecessor[:], index.WriteBatch{}); err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
		replacement := [1][]sqltype.Value{row}
		batch := index.WriteBatch{Values: replacement[:], ActionMode: actionMode}
		batch.Set(0, proposal)
		if err = dml.ReplaceRows(s.writeHost(), ref, predecessor[:], batch, dml.ReplaceMerge); err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
	}
	if action == parser.ForeignKeySetDefault {
		// RI SET DEFAULT performs its NO ACTION recheck even if the new datum is
		// unchanged. This preserves the parent-side diagnostic for a deleted default.
		final, err := s.captureForeignKeyRows(ref, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		parentRef := e.catalog.Domain.Ref(auth.Catalog.Epoch(), job.Parent.Address())
		parents, err := s.captureForeignKeyRows(parentRef, job.Definition.Index)
		if err != nil {
			return err
		}
		return final.Each(func(child storage.RowVersion) (bool, error) {
			match, err := table.ForeignKeyRowsMatch(s.op.Context(), job.Comparisons, child.Values, event.Old.Values, s.input)
			if err != nil || !match {
				return false, err
			}
			found, err := s.foreignKeyParentExists(parentRef, *job, &parents, child.Values, false)
			if err != nil || found {
				return false, err
			}
			return false, table.ForeignKeyDiagnostic(s.op.Context(), reportAuth.View, reportAuth, job.Child, job.Parent, job.Definition, event.Old.Values, true, false, s.output)
		})
	}
	return nil
}

// Context describes the real internally executed plan; it is not parsed SQL or
// a second dispatcher. AFTER-event diagnostics deliberately have no SPI frame.
func (s *StatementScope) foreignActionError(err error, job *dml.ForeignKeyJob, event dml.ForeignActionTransition, action parser.ForeignKeyAction, view catalog.Read) error {
	err = s.op.CommandError(err)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code == "23503" || d.Code == "23001" || d.Code == "23P01" {
		return err
	}
	copy := *d
	context := `SQL statement "` + dml.ForeignActionQuery(job, event, action, view) + `"`
	if copy.Context != "" {
		copy.Context += "\n"
	}
	copy.Context += context
	return &copy
}
