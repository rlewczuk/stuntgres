package dml

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// planForeignAction is job's cached SET/CASCADE plan for the event's
// direction: the target slots, the input environment (with SET DEFAULT's
// bound defaults, from the host), SET NULL's domain inputs and CASCADE's
// parent slots and coercions.
func planForeignAction(h FinishHost, auth Authority, job *ForeignKeyJob, event ForeignActionTransition, action parser.ForeignKeyAction) (*ForeignActionPlan, error) {
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
	plan := &ForeignActionPlan{Targets: make([]int, len(attributes))}
	for i, a := range attributes {
		plan.Targets[i] = slices.Index(job.Child.Attributes, a)
		if plan.Targets[i] < 0 {
			return nil, ids.ErrCatalogReplaced
		}
	}
	for _, slot := range plan.Targets {
		plan.DomainChecks = plan.DomainChecks || sqltype.ContainsDomainConstraints(job.Child.Columns[slot].Usage.Type)
	}
	if err := h.PlanForeignActionInput(auth, job, plan, event, action); err != nil {
		return nil, err
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

// applyForeignAction runs one referential action of job for a parent
// transition: RESTRICT reports a still-matching child; CASCADE, SET NULL and
// SET DEFAULT grant, re-read, revalidate and stage each matching child as the
// child's owner. SET DEFAULT then rechecks the new keys like NO ACTION.
func applyForeignAction(h FinishHost, auth Authority, job *ForeignKeyJob, event ForeignActionTransition, action parser.ForeignKeyAction) (failure error) {
	view := auth.Access().Catalog
	ref := view.Domain().Ref(view.Epoch(), job.Child.Address())
	if err := h.AdmitForeignAction(ref, job, action != parser.ForeignKeyRestrict); err != nil {
		return err
	}
	h.EnterForeignKeyTrigger()
	defer h.LeaveForeignKeyTrigger()
	input, output := h.Environments()
	reportAuth := auth.ActingAs(event.Owner)
	if action == parser.ForeignKeyRestrict {
		children, err := h.ForeignKeyRows(ref, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		return children.Each(func(child storage.RowVersion) (bool, error) {
			match, err := table.ForeignKeyRowsMatch(h.Context(), job.Comparisons, child.Values, event.Old.Values, input)
			if err != nil || !match {
				return false, err
			}
			if err = h.GrantForeignKey(ref, child.Row, lock.ForKeyShare, children.Storage); err != nil {
				return false, err
			}
			latest, live, err := h.ForeignActionRow(ref, child)
			if err != nil || !live {
				return false, err
			}
			if err := h.PhysicalRowEvent(ref, latest, lock.MultiKeyShare, 0); err != nil {
				return false, err
			}
			match, err = table.ForeignKeyRowsMatch(h.Context(), job.Comparisons, latest.Values, event.Old.Values, input)
			if err != nil || !match {
				return false, err
			}
			d := table.ForeignKeyDiagnostic(h.Context(), reportAuth.Access(), reportAuth, job.Child, job.Parent, job.Definition, event.Old.Values, true, false, output)
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
	ownerAuth := auth.ActingAs(job.Child.Owner)
	previous := h.SwapForeignActionAuthority(ownerAuth)
	defer h.SwapForeignActionAuthority(previous)
	deletion := action == parser.ForeignKeyCascade && event.Next.Row == 0
	var plan *ForeignActionPlan
	var err error
	if !deletion {
		plan, err = planForeignAction(h, ownerAuth, job, event, action)
		if err != nil {
			return foreignActionError(h, err, job, event, action, view)
		}
	}
	// RI executes with SPI's fire_triggers=false: each generated command owns
	// producer qualification, but queues AFTER bodies at the caller's query
	// depth. Do not drain here or split same-target statement supersession.
	statementEvent := stored.TriggerUpdate
	binding := analyze.Binding{Ref: ref, Relation: job.Child}
	if deletion {
		statementEvent = stored.TriggerDelete
	} else {
		binding.Targets = plan.Targets
	}
	if err := BeforeStatement(h, binding, statementEvent); err != nil {
		return err
	}
	if err := RegisterAfterStatement(h, binding, statementEvent); err != nil {
		return err
	}
	defer func() {
		if failure == nil {
			if err := h.CompleteAfterStatement(ref, statementEvent); err != nil {
				failure = foreignActionError(h, err, job, event, action, view)
			}
		}
	}()
	children, err := h.ForeignKeyRows(ref, ids.ObjectAddress{})
	if err != nil {
		return err
	}
	retained, err := h.RetainedForeignKeyRows(ref, children)
	if err != nil {
		return err
	}
	// Retained matches take precedence by real RowID. The canonical grant and
	// live-successor crosscheck below run before any latest-key match/staging.
	var candidates []storage.RowVersion
	var retainedMatches map[ids.RowID]struct{}
	if retained.Storage != nil {
		err = retained.Each(func(child storage.RowVersion) (bool, error) {
			match, err := table.ForeignKeyRowsMatch(h.Context(), job.Comparisons, child.Values, event.Old.Values, input)
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
		match, err := table.ForeignKeyRowsMatch(h.Context(), job.Comparisons, child.Values, event.Old.Values, input)
		if match {
			candidates = append(candidates, child)
		}
		return false, err
	})
	if err != nil {
		return err
	}
	planInput := input
	if plan != nil {
		planInput = plan.Input
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: h.Context(), Host: h, Catalog: ownerAuth.Access().Catalog, Input: planInput, Output: output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: ownerAuth}}
	if plan != nil && plan.DomainChecks {
		evaluation.Input.DomainChecks = &evaluation
	}
	schema := view.Get(job.Child.Namespace()).Name()
	for _, candidate := range candidates {
		mode := lock.ForNoKeyUpdate
		if deletion {
			mode = lock.ForUpdate
		}
		if err = h.GrantForeignKey(ref, candidate.Row, mode, children.Storage); err != nil {
			return foreignActionError(h, err, job, event, action, view)
		}
		if err = h.RefreshForeignActionJob(job); err != nil {
			return err
		}
		old, live, rowErr := h.ForeignActionRow(ref, candidate)
		if rowErr != nil {
			return foreignActionError(h, rowErr, job, event, action, view)
		}
		if !live {
			continue
		}
		if old.Version != candidate.Version {
			lockMode, err := lock.MultiLockMode(mode)
			if err != nil {
				return err
			}
			if err := h.PhysicalRowEvent(ref, old, lockMode, 0); err != nil {
				return foreignActionError(h, err, job, event, action, view)
			}
		}
		match, err := table.ForeignKeyRowsMatch(h.Context(), job.Comparisons, old.Values, event.Old.Values, input)
		if err != nil {
			return err
		}
		if !match {
			continue
		}
		if deletion {
			before, failure := Before(h, job.Child, BeforeRow{Relation: ref, Event: BeforeDelete, Old: old.Values})
			if failure != nil {
				return foreignActionError(h, failure, job, event, action, view)
			}
			if !before.Proceed {
				continue
			}
			if err = h.DeleteForeignAction(ref, old); err != nil {
				return foreignActionError(h, err, job, event, action, view)
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
				return foreignActionError(h, err, job, event, action, view)
			}
		}
		if job.Child.HasTriggers() {
			targets := UpdateTargetAttributes(h, ref, job.Child, plan.Targets, nil)
			before, failure := Before(h, job.Child, BeforeRow{Relation: ref, Event: BeforeUpdate, Old: old.Values, New: row, Targets: targets})
			if failure != nil {
				return foreignActionError(h, failure, job, event, action, view)
			}
			if !before.Proceed {
				continue
			}
			row = before.Row
		}
		b := analyze.Binding{Ref: ref, Relation: job.Child, Targets: plan.Targets}
		if err = ValidateMutationRow(h, b, schema, row, &evaluation); err != nil {
			return foreignActionError(h, err, job, event, action, view)
		}
		keyChanged, err := UniqueKeyChanged(h, ref, old.Values, row)
		if err != nil {
			return foreignActionError(h, err, job, event, action, view)
		}
		if keyChanged {
			if err = h.GrantForeignKey(ref, old.Row, lock.ForUpdate, children.Storage); err != nil {
				return foreignActionError(h, err, job, event, action, view)
			}
		}
		if err = ClaimUniqueRow(h, ref, old); err != nil {
			return foreignActionError(h, err, job, event, action, view)
		}
		old.PendingKeyChange = old.PendingKeyChange || keyChanged
		actionMode := lock.MultiNoKeyAction
		if keyChanged {
			actionMode = lock.MultiUpdateAction
		}
		if err := h.PhysicalRowEvent(ref, old, actionMode, 0); err != nil {
			return foreignActionError(h, err, job, event, action, view)
		}
		predecessor := [1]storage.RowVersion{old}
		proposal := index.WriteCandidate{Values: row}
		if err = CheckUniqueRow(h, ref, &proposal, predecessor[:], index.WriteBatch{}); err != nil {
			return foreignActionError(h, err, job, event, action, view)
		}
		replacement := [1][]sqltype.Value{row}
		batch := index.WriteBatch{Values: replacement[:], ActionMode: actionMode}
		batch.Set(0, proposal)
		if err = ReplaceRows(h, ref, predecessor[:], batch, ReplaceMerge); err != nil {
			return foreignActionError(h, err, job, event, action, view)
		}
	}
	if action == parser.ForeignKeySetDefault {
		// RI SET DEFAULT performs its NO ACTION recheck even if the new datum is
		// unchanged. This preserves the parent-side diagnostic for a deleted default.
		final, err := h.ForeignKeyRows(ref, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		parentRef := view.Domain().Ref(view.Epoch(), job.Parent.Address())
		parents, err := h.ForeignKeyRows(parentRef, job.Definition.Index)
		if err != nil {
			return err
		}
		return final.Each(func(child storage.RowVersion) (bool, error) {
			match, err := table.ForeignKeyRowsMatch(h.Context(), job.Comparisons, child.Values, event.Old.Values, input)
			if err != nil || !match {
				return false, err
			}
			found, err := foreignKeyParentExists(h, parentRef, *job, &parents, child.Values, false)
			if err != nil || found {
				return false, err
			}
			return false, table.ForeignKeyDiagnostic(h.Context(), reportAuth.Access(), reportAuth, job.Child, job.Parent, job.Definition, event.Old.Values, true, false, output)
		})
	}
	return nil
}

// foreignActionError adds the internally executed action's statement to the
// context of a non-FK error. The context describes the real plan; it is not
// parsed SQL or a second dispatcher. AFTER-event diagnostics deliberately have
// no SPI frame.
func foreignActionError(h FinishHost, err error, job *ForeignKeyJob, event ForeignActionTransition, action parser.ForeignKeyAction, view catalog.Read) error {
	err = h.CommandError(err)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code == "23503" || d.Code == "23001" || d.Code == "23P01" {
		return err
	}
	copy := *d
	context := `SQL statement "` + ForeignActionQuery(job, event, action, view) + `"`
	if copy.Context != "" {
		copy.Context += "\n"
	}
	copy.Context += context
	return &copy
}
