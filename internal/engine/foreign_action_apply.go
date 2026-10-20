package engine

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type foreignActionPlan struct {
	targets, parentSlots []int
	defaults             []expr.Expr
	defaultOrigins       []defaultOrigin
	conversions          []sqltype.CoercionPlan
	convert              []bool
	input                sqltype.InputEnvironment
	domainChecks         bool
}

func (s *StatementScope) planForeignAction(auth AuthorizationView, job *foreignKeyJob, event foreignActionTransition, action parser.ForeignKeyAction) (*foreignActionPlan, error) {
	cached := job.deletePlan
	if event.next.Row != 0 {
		cached = job.updatePlan
	}
	if cached != nil {
		return cached, nil
	}
	attributes := job.definition.Attributes
	if event.next.Row == 0 && len(job.definition.DeleteAttributes) != 0 {
		attributes = job.definition.DeleteAttributes
	}
	plan := &foreignActionPlan{targets: make([]int, len(attributes))}
	for i, a := range attributes {
		plan.targets[i] = slices.Index(job.child.Attributes, a)
		if plan.targets[i] < 0 {
			return nil, ids.ErrCatalogReplaced
		}
	}
	plan.input = s.input
	for _, slot := range plan.targets {
		plan.domainChecks = plan.domainChecks || sqltype.ContainsDomainConstraints(job.child.Columns[slot].Usage.Type)
	}
	if plan.domainChecks {
		e := s.op.session.Engine()
		e.mu.Lock()
		name, err := e.captureTypeDiagnosticName(auth)
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		plan.input.TypeDiagnosticName = name
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: plan.input, Output: s.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	if plan.domainChecks {
		evaluation.Input.DomainChecks = &evaluation
	}
	if action == parser.ForeignKeySetDefault {
		b := binding{Relation: job.child}
		e := s.op.session.Engine()
		bindInsertDefaults(e.catalog.owner(), &b, auth, parser.OverrideNone)
		plan.defaults = make([]expr.Expr, len(plan.targets))
		plan.defaultOrigins = make([]defaultOrigin, len(plan.targets))
		for i, slot := range plan.targets {
			if len(b.Insert.Defaults) != 0 {
				plan.defaults[i] = b.Insert.Defaults[slot]
				plan.defaultOrigins[i] = bindingDefaultOrigin(&b, slot)
			}
		}
		// Retained defaults can themselves cast to a checked domain even when
		// the action destination is an ordinary base type.
		binder := expressionBinder{host: e.analysisHost(s.op), ctx: s.op.Context(), input: plan.input, query: queryScopeState{auth: &auth}}
		e.mu.Lock()
		err := binder.captureRetainedWriteInput(plan.defaults, nil, nil)
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		plan.input, evaluation.Input = binder.input, binder.input
		if plan.domainChecks {
			evaluation.Input.DomainChecks = &evaluation
		}
		for i, expression := range plan.defaults {
			if expression == nil {
				continue
			}
			defaultEvaluation := evaluation
			applyDefaultOrigin(bindingDefaultOrigin(&b, plan.targets[i]), &defaultEvaluation)
			folded, _, err := preflightCoercionsWithPolicy(&defaultEvaluation, expression, true)
			if err != nil {
				return nil, err
			}
			if err := validateExpressionJoinExecution(&defaultEvaluation, folded, auth.Catalog); err != nil {
				return nil, err
			}
			plan.defaults[i] = folded
		}
	}
	if action == parser.ForeignKeySetNull && plan.domainChecks {
		plan.defaults = make([]expr.Expr, len(plan.targets))
		for i, slot := range plan.targets {
			if job.child.Columns[slot].Usage.Type.HasDomainConstraints() {
				plan.defaults[i] = domainNullInputExpression(job.child.Columns[slot].Usage)
			}
		}
	}
	if action == parser.ForeignKeyCascade {
		plan.parentSlots = make([]int, len(attributes))
		plan.conversions = make([]sqltype.CoercionPlan, len(attributes))
		plan.convert = make([]bool, len(attributes))
		for i, a := range attributes {
			at := slices.Index(job.definition.Attributes, a)
			parentSlot := slices.Index(job.parent.Attributes, job.definition.Referenced[at])
			if parentSlot < 0 {
				return nil, ids.ErrCatalogReplaced
			}
			plan.parentSlots[i] = parentSlot
			source, dest := job.parent.Columns[parentSlot].Usage, job.child.Columns[plan.targets[i]].Usage
			if source != dest {
				var err error
				plan.conversions[i], err = sqltype.PlanCoercion(source, dest, sqltype.Assignment)
				if err != nil {
					return nil, err
				}
				plan.convert[i] = true
			}
		}
	}
	if event.next.Row == 0 {
		job.deletePlan = plan
	} else {
		job.updatePlan = plan
	}
	return plan, nil
}

func (s *StatementScope) applyForeignAction(auth AuthorizationView, job *foreignKeyJob, event foreignActionTransition, action parser.ForeignKeyAction) error {
	e := s.op.session.Engine()
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), job.child.Address())
	if err := s.admitForeignAction(ref, job, action != parser.ForeignKeyRestrict); err != nil {
		return err
	}
	reportAuth := foreignActionOwner(auth, event.owner)
	if action == parser.ForeignKeyRestrict {
		children, err := s.captureForeignKeyRows(ref, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		return children.each(func(child storage.RowVersion) (bool, error) {
			match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, child.Values, event.old.Values, s.input)
			if err != nil || !match {
				return false, err
			}
			if err = s.foreignKeyGrant(ref, child.Row, lock.ForKeyShare, children.storage); err != nil {
				return false, err
			}
			latest, live, err := s.foreignActionRow(ref, child)
			if err != nil || !live {
				return false, err
			}
			if err := s.physicalRowEvent(ref, latest, lock.MultiKeyShare, 0); err != nil {
				return false, err
			}
			match, err = foreignKeyRowsMatch(s.op.Context(), job.comparisons, latest.Values, event.old.Values, s.input)
			if err != nil || !match {
				return false, err
			}
			d := foreignKeyDiagnostic(s.op.Context(), reportAuth, job.child, job.parent, job.definition, event.old.Values, true, false, s.output)
			var diagnostic *sqlstate.Error
			if !errors.As(d, &diagnostic) {
				return false, d
			}
			copy := *diagnostic
			copy.Code = "23001"
			copy.Message = fmt.Sprintf(`update or delete on table "%s" violates RESTRICT setting of foreign key constraint "%s" on table "%s"`, job.parent.Name(), job.definition.Name, job.child.Name())
			copy.Detail = strings.Replace(copy.Detail, " is still referenced", " is referenced", 1)
			return false, &copy
		})
	}
	ownerAuth := foreignActionOwner(auth, job.child.Owner)
	previous := s.foreignActionAuth
	s.foreignActionAuth = &ownerAuth
	defer func() { s.foreignActionAuth = previous }()
	deletion := action == parser.ForeignKeyCascade && event.next.Row == 0
	var plan *foreignActionPlan
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
	if retained.storage != nil {
		err = retained.each(func(child storage.RowVersion) (bool, error) {
			match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, child.Values, event.old.Values, s.input)
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
	err = children.each(func(child storage.RowVersion) (bool, error) {
		if _, exists := retainedMatches[child.Row]; exists {
			return false, nil
		}
		match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, child.Values, event.old.Values, s.input)
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
		input = plan.input
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: ownerAuth.Catalog, Input: input, Output: s.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &ownerAuth}}
	if plan != nil && plan.domainChecks {
		evaluation.Input.DomainChecks = &evaluation
	}
	schema := auth.Catalog.Get(job.child.Namespace()).Name()
	for _, candidate := range candidates {
		mode := lock.ForNoKeyUpdate
		if deletion {
			mode = lock.ForUpdate
		}
		if err = s.foreignKeyGrant(ref, candidate.Row, mode, children.storage); err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
		e.mu.Lock()
		current := e.privateCatalogLocked(s.owner.catalog)
		err = refreshForeignActionJob(current, job)
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
		match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, old.Values, event.old.Values, s.input)
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
		for i, slot := range plan.targets {
			switch action {
			case parser.ForeignKeyCascade:
				row[slot] = event.next.Values[plan.parentSlots[i]]
				if plan.convert[i] {
					row[slot], err = plan.conversions[i].Apply(evaluation.Context, row[slot], evaluation.Input, evaluation.Output)
				}
			case parser.ForeignKeySetNull, parser.ForeignKeySetDefault:
				row[slot] = sqltype.Null(job.child.Columns[slot].Usage.Type)
				if len(plan.defaults) != 0 && plan.defaults[i] != nil {
					if len(plan.defaultOrigins) != 0 {
						row[slot], err = evalDefaultExpression(&evaluation, plan.defaults[i], plan.defaultOrigins[i])
					} else {
						row[slot], err = plan.defaults[i].Eval(&evaluation)
					}
				}
			}
			if err != nil {
				return s.foreignActionError(err, job, event, action, auth.Catalog)
			}
		}
		b := binding{Ref: ref, Relation: job.child, Targets: plan.targets}
		if err = e.validateMutationRow(s, b, schema, row, &evaluation); err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
		keyChanged, err := s.uniqueKeyChanged(ref, old.Values, row)
		if err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
		if keyChanged {
			if err = s.foreignKeyGrant(ref, old.Row, lock.ForUpdate, children.storage); err != nil {
				return s.foreignActionError(err, job, event, action, auth.Catalog)
			}
		}
		if err = s.claimUniqueRow(ref, old); err != nil {
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
		proposal := indexWriteCandidate{values: row}
		if err = s.checkUniqueRow(ref, &proposal, predecessor[:], indexWriteBatch{}); err != nil {
			return s.foreignActionError(err, job, event, action, auth.Catalog)
		}
		replacement := [1][]sqltype.Value{row}
		batch := indexWriteBatch{values: replacement[:], actionMode: actionMode}
		batch.set(0, proposal)
		if err = s.stage.replace(ref, predecessor[:], batch); err != nil {
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
		parentRef := e.catalog.Domain.Ref(auth.Catalog.Epoch(), job.parent.Address())
		parents, err := s.captureForeignKeyRows(parentRef, job.definition.Index)
		if err != nil {
			return err
		}
		return final.each(func(child storage.RowVersion) (bool, error) {
			match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, child.Values, event.old.Values, s.input)
			if err != nil || !match {
				return false, err
			}
			found, err := s.foreignKeyParentExists(parentRef, *job, &parents, child.Values, false)
			if err != nil || found {
				return false, err
			}
			return false, foreignKeyDiagnostic(s.op.Context(), reportAuth, job.child, job.parent, job.definition, event.old.Values, true, false, s.output)
		})
	}
	return nil
}

// Context describes the real internally executed plan; it is not parsed SQL or
// a second dispatcher. AFTER-event diagnostics deliberately have no SPI frame.
func (s *StatementScope) foreignActionError(err error, job *foreignKeyJob, event foreignActionTransition, action parser.ForeignKeyAction, view catalog.Read) error {
	err = s.op.CommandError(err)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code == "23503" || d.Code == "23001" || d.Code == "23P01" {
		return err
	}
	copy := *d
	context := `SQL statement "` + foreignActionQuery(job, event, action, view) + `"`
	if copy.Context != "" {
		copy.Context += "\n"
	}
	copy.Context += context
	return &copy
}

func foreignActionQuery(job *foreignKeyJob, event foreignActionTransition, action parser.ForeignKeyAction, view catalog.Read) string {
	var q strings.Builder
	deletion := action == parser.ForeignKeyCascade && event.next.Row == 0
	if deletion {
		q.WriteString("DELETE FROM ONLY ")
	} else {
		q.WriteString("UPDATE ONLY ")
	}
	q.WriteString(catalog.QuoteIdentifier(view.Get(job.child.Namespace()).Name()))
	q.WriteByte('.')
	q.WriteString(catalog.QuoteIdentifier(job.child.Name()))
	offset := 0
	if !deletion {
		q.WriteString(" SET ")
		attributes := job.definition.Attributes
		if event.next.Row == 0 && len(job.definition.DeleteAttributes) != 0 {
			attributes = job.definition.DeleteAttributes
		}
		for i, a := range attributes {
			if i != 0 {
				q.WriteString(", ")
			}
			q.WriteString(catalog.QuoteIdentifier(job.child.Columns[slices.Index(job.child.Attributes, a)].Name))
			q.WriteString(" = ")
			switch action {
			case parser.ForeignKeyCascade:
				q.WriteByte('$')
				q.WriteString(strconv.Itoa(i + 1))
				offset++
			case parser.ForeignKeySetNull:
				q.WriteString("NULL")
			case parser.ForeignKeySetDefault:
				q.WriteString("DEFAULT")
			}
		}
	}
	q.WriteString(" WHERE ")
	for i, a := range job.definition.Attributes {
		if i != 0 {
			q.WriteString(" AND ")
		}
		childSlot := slices.Index(job.child.Attributes, a)
		parentSlot := slices.Index(job.parent.Attributes, job.definition.Referenced[i])
		var comparison sqltype.IndexForeignKeyComparison
		for _, c := range job.comparisons {
			if c.child == childSlot {
				comparison = c.comparison
				break
			}
		}
		parentInput, childInput := comparison.InputTypes()
		q.WriteByte('$')
		q.WriteString(strconv.Itoa(offset + i + 1))
		if job.parent.Columns[parentSlot].Usage.Type != parentInput {
			q.WriteString("::")
			q.WriteString(pgIdentifier(parentInput.NamespaceName()))
			q.WriteByte('.')
			q.WriteString(pgIdentifier(parentInput.Name()))
		}
		q.WriteString(" OPERATOR(pg_catalog.=) ")
		q.WriteString(catalog.QuoteIdentifier(job.child.Columns[childSlot].Name))
		if job.child.Columns[childSlot].Usage.Type != childInput {
			q.WriteString("::")
			q.WriteString(pgIdentifier(childInput.NamespaceName()))
			q.WriteByte('.')
			q.WriteString(pgIdentifier(childInput.Name()))
		}
	}
	return q.String()
}
