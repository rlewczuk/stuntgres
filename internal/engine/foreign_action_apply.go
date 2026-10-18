package engine

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type foreignActionPlan struct {
	targets, parentSlots []int
	defaults             []BoundExpr
	conversions          []sqltype.CoercionPlan
	convert              []bool
	input                sqltype.InputEnvironment
	domainChecks         bool
}

func (s *StatementScope) planForeignAction(auth AuthorizationView, job *foreignKeyJob, event foreignActionTransition, action parser.ForeignKeyAction) (*foreignActionPlan, error) {
	cached := job.deletePlan
	if event.next.row != 0 {
		cached = job.updatePlan
	}
	if cached != nil {
		return cached, nil
	}
	attributes := job.definition.Attributes
	if event.next.row == 0 && len(job.definition.DeleteAttributes) != 0 {
		attributes = job.definition.DeleteAttributes
	}
	plan := &foreignActionPlan{targets: make([]int, len(attributes))}
	for i, a := range attributes {
		plan.targets[i] = slices.Index(job.child.attributes, a)
		if plan.targets[i] < 0 {
			return nil, ErrCatalogReplaced
		}
	}
	plan.input = s.input
	for _, slot := range plan.targets {
		plan.domainChecks = plan.domainChecks || sqltype.ContainsDomainChecks(job.child.columns[slot].Usage.Type)
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
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1, statementScope: s, catalog: auth.catalog, Input: plan.input, Output: s.output, routineAuthority: &auth}
	if plan.domainChecks {
		evaluation.Input.DomainChecks = &evaluation
	}
	if action == parser.ForeignKeySetDefault {
		b := binding{relation: job.child}
		e := s.op.session.Engine()
		e.bindInsertDefaults(&b, auth, parser.OverrideNone)
		plan.defaults = make([]BoundExpr, len(plan.targets))
		for i, slot := range plan.targets {
			if len(b.defaults) != 0 {
				plan.defaults[i] = b.defaults[slot]
			}
		}
		// Retained defaults can themselves cast to a checked domain even when
		// the action destination is an ordinary base type.
		binder := expressionBinder{engine: e, ctx: s.op.Context(), input: plan.input, op: s.op, queryAuth: &auth}
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
			defaultEvaluation.routineRecipe, defaultEvaluation.routineGraft = true, false
			defaultEvaluation.routineReferences = job.child.defaults[job.child.attributes[plan.targets[i]]].dependencies
			defaultEvaluation.routineConsumer = job.child
			folded, _, err := preflightCoercionsWithPolicy(&defaultEvaluation, expression, true)
			if err != nil {
				return nil, err
			}
			if err := validateExpressionJoinExecution(&defaultEvaluation, folded, auth.catalog); err != nil {
				return nil, err
			}
			plan.defaults[i] = folded
		}
	}
	if action == parser.ForeignKeySetNull && plan.domainChecks {
		plan.defaults = make([]BoundExpr, len(plan.targets))
		for i, slot := range plan.targets {
			if job.child.columns[slot].Usage.Type.HasDomainChecks() {
				plan.defaults[i] = domainNullInputExpression(job.child.columns[slot].Usage)
			}
		}
	}
	if action == parser.ForeignKeyCascade {
		plan.parentSlots = make([]int, len(attributes))
		plan.conversions = make([]sqltype.CoercionPlan, len(attributes))
		plan.convert = make([]bool, len(attributes))
		for i, a := range attributes {
			at := slices.Index(job.definition.Attributes, a)
			parentSlot := slices.Index(job.parent.attributes, job.definition.Referenced[at])
			if parentSlot < 0 {
				return nil, ErrCatalogReplaced
			}
			plan.parentSlots[i] = parentSlot
			source, dest := job.parent.columns[parentSlot].Usage, job.child.columns[plan.targets[i]].Usage
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
	if event.next.row == 0 {
		job.deletePlan = plan
	} else {
		job.updatePlan = plan
	}
	return plan, nil
}

func (s *StatementScope) applyForeignAction(auth AuthorizationView, job *foreignKeyJob, event foreignActionTransition, action parser.ForeignKeyAction) error {
	e := s.op.session.Engine()
	ref := e.domain.Ref(auth.catalog.Epoch(), job.child.address)
	if err := s.admitForeignAction(ref, job, action != parser.ForeignKeyRestrict); err != nil {
		return err
	}
	reportAuth := foreignActionOwner(auth, event.owner)
	if action == parser.ForeignKeyRestrict {
		children, err := s.captureForeignKeyRows(ref, ObjectAddress{})
		if err != nil {
			return err
		}
		return children.each(func(child rowVersion) (bool, error) {
			match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, child.values, event.old.values, s.input)
			if err != nil || !match {
				return false, err
			}
			if err = s.foreignKeyGrant(ref, child.row, keyShare, children.storage); err != nil {
				return false, err
			}
			latest, live, err := s.foreignActionRow(ref, child)
			if err != nil || !live {
				return false, err
			}
			if err := s.physicalRowEvent(ref, latest, multiKeyShare, 0); err != nil {
				return false, err
			}
			match, err = foreignKeyRowsMatch(s.op.Context(), job.comparisons, latest.values, event.old.values, s.input)
			if err != nil || !match {
				return false, err
			}
			d := foreignKeyDiagnostic(s.op.Context(), reportAuth, job.child, job.parent, job.definition, event.old.values, true, false, s.output)
			var diagnostic *sqlstate.Error
			if !errors.As(d, &diagnostic) {
				return false, d
			}
			copy := *diagnostic
			copy.Code = "23001"
			copy.Message = fmt.Sprintf(`update or delete on table "%s" violates RESTRICT setting of foreign key constraint "%s" on table "%s"`, job.parent.name, job.definition.Name, job.child.name)
			copy.Detail = strings.Replace(copy.Detail, " is still referenced", " is referenced", 1)
			return false, &copy
		})
	}
	ownerAuth := foreignActionOwner(auth, job.child.owner)
	previous := s.foreignActionAuth
	s.foreignActionAuth = &ownerAuth
	defer func() { s.foreignActionAuth = previous }()
	deletion := action == parser.ForeignKeyCascade && event.next.row == 0
	var plan *foreignActionPlan
	var err error
	if !deletion {
		plan, err = s.planForeignAction(ownerAuth, job, event, action)
		if err != nil {
			return s.foreignActionError(err, job, event, action, auth.catalog)
		}
	}
	children, retained, err := s.captureForeignActionRows(ref)
	if err != nil {
		return err
	}
	// Retained matches take precedence by real RowID. The canonical grant and
	// live-successor crosscheck below run before any latest-key match/staging.
	var candidates []rowVersion
	var retainedMatches map[RowID]struct{}
	if retained.storage != nil {
		err = retained.each(func(child rowVersion) (bool, error) {
			match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, child.values, event.old.values, s.input)
			if err != nil || !match {
				return false, err
			}
			if retainedMatches == nil {
				retainedMatches = make(map[RowID]struct{})
			}
			retainedMatches[child.row] = struct{}{}
			candidates = append(candidates, child)
			return false, nil
		})
		if err != nil {
			return err
		}
	}
	err = children.each(func(child rowVersion) (bool, error) {
		if _, exists := retainedMatches[child.row]; exists {
			return false, nil
		}
		match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, child.values, event.old.values, s.input)
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
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1, statementScope: s, catalog: ownerAuth.catalog, Input: input, Output: s.output, routineAuthority: &ownerAuth}
	if plan != nil && plan.domainChecks {
		evaluation.Input.DomainChecks = &evaluation
	}
	schema := auth.catalog.state.descriptors[job.child.namespace].Name()
	for _, candidate := range candidates {
		mode := noKeyUpdate
		if deletion {
			mode = rowUpdate
		}
		if err = s.foreignKeyGrant(ref, candidate.row, mode, children.storage); err != nil {
			return s.foreignActionError(err, job, event, action, auth.catalog)
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
			return s.foreignActionError(rowErr, job, event, action, auth.catalog)
		}
		if !live {
			continue
		}
		if old.version != candidate.version {
			lockMode, err := multiLockMode(mode)
			if err != nil {
				return err
			}
			if err := s.physicalRowEvent(ref, old, lockMode, 0); err != nil {
				return s.foreignActionError(err, job, event, action, auth.catalog)
			}
		}
		match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, old.values, event.old.values, s.input)
		if err != nil {
			return err
		}
		if !match {
			continue
		}
		if deletion {
			if err = s.stage.deleteForeignAction(ref, old); err != nil {
				return s.foreignActionError(err, job, event, action, auth.catalog)
			}
			continue
		}
		row := slices.Clone(old.values)
		evaluation.Row = old.values
		for i, slot := range plan.targets {
			switch action {
			case parser.ForeignKeyCascade:
				row[slot] = event.next.values[plan.parentSlots[i]]
				if plan.convert[i] {
					row[slot], err = plan.conversions[i].Apply(evaluation.Context, row[slot], evaluation.Input, evaluation.Output)
				}
			case parser.ForeignKeySetNull, parser.ForeignKeySetDefault:
				row[slot] = sqltype.Null(job.child.columns[slot].Usage.Type)
				if len(plan.defaults) != 0 && plan.defaults[i] != nil {
					row[slot], err = plan.defaults[i].Eval(&evaluation)
				}
			}
			if err != nil {
				return s.foreignActionError(err, job, event, action, auth.catalog)
			}
		}
		b := binding{ref: ref, relation: job.child, targets: plan.targets}
		if err = e.validateMutationRow(s, b, schema, row, &evaluation); err != nil {
			return s.foreignActionError(err, job, event, action, auth.catalog)
		}
		keyChanged, err := s.uniqueKeyChanged(ref, old.values, row)
		if err != nil {
			return s.foreignActionError(err, job, event, action, auth.catalog)
		}
		if keyChanged {
			if err = s.foreignKeyGrant(ref, old.row, rowUpdate, children.storage); err != nil {
				return s.foreignActionError(err, job, event, action, auth.catalog)
			}
		}
		if err = s.claimUniqueRow(ref, old); err != nil {
			return s.foreignActionError(err, job, event, action, auth.catalog)
		}
		old.pendingKeyChange = old.pendingKeyChange || keyChanged
		actionMode := multiNoKeyAction
		if keyChanged {
			actionMode = multiUpdateAction
		}
		if err := s.physicalRowEvent(ref, old, actionMode, 0); err != nil {
			return s.foreignActionError(err, job, event, action, auth.catalog)
		}
		predecessor := [1]rowVersion{old}
		proposal := indexWriteCandidate{values: row}
		if err = s.checkUniqueRow(ref, &proposal, predecessor[:], indexWriteBatch{}); err != nil {
			return s.foreignActionError(err, job, event, action, auth.catalog)
		}
		replacement := [1][]sqltype.Value{row}
		batch := indexWriteBatch{values: replacement[:], actionMode: actionMode}
		batch.set(0, proposal)
		if err = s.stage.replace(ref, predecessor[:], batch); err != nil {
			return s.foreignActionError(err, job, event, action, auth.catalog)
		}
	}
	if action == parser.ForeignKeySetDefault {
		// RI SET DEFAULT performs its NO ACTION recheck even if the new datum is
		// unchanged. This preserves the parent-side diagnostic for a deleted default.
		final, err := s.captureForeignKeyRows(ref, ObjectAddress{})
		if err != nil {
			return err
		}
		parentRef := e.domain.Ref(auth.catalog.Epoch(), job.parent.address)
		parents, err := s.captureForeignKeyRows(parentRef, job.definition.Index)
		if err != nil {
			return err
		}
		return final.each(func(child rowVersion) (bool, error) {
			match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, child.values, event.old.values, s.input)
			if err != nil || !match {
				return false, err
			}
			found, err := s.foreignKeyParentExists(parentRef, *job, &parents, child.values, false)
			if err != nil || found {
				return false, err
			}
			return false, foreignKeyDiagnostic(s.op.Context(), reportAuth, job.child, job.parent, job.definition, event.old.values, true, false, s.output)
		})
	}
	return nil
}

// Context describes the real internally executed plan; it is not parsed SQL or
// a second dispatcher. AFTER-event diagnostics deliberately have no SPI frame.
func (s *StatementScope) foreignActionError(err error, job *foreignKeyJob, event foreignActionTransition, action parser.ForeignKeyAction, view CatalogRead) error {
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

func foreignActionQuery(job *foreignKeyJob, event foreignActionTransition, action parser.ForeignKeyAction, view CatalogRead) string {
	var q strings.Builder
	deletion := action == parser.ForeignKeyCascade && event.next.row == 0
	if deletion {
		q.WriteString("DELETE FROM ONLY ")
	} else {
		q.WriteString("UPDATE ONLY ")
	}
	q.WriteString(quoteIdentifier(view.state.descriptors[job.child.namespace].Name()))
	q.WriteByte('.')
	q.WriteString(quoteIdentifier(job.child.name))
	offset := 0
	if !deletion {
		q.WriteString(" SET ")
		attributes := job.definition.Attributes
		if event.next.row == 0 && len(job.definition.DeleteAttributes) != 0 {
			attributes = job.definition.DeleteAttributes
		}
		for i, a := range attributes {
			if i != 0 {
				q.WriteString(", ")
			}
			q.WriteString(quoteIdentifier(job.child.columns[slices.Index(job.child.attributes, a)].Name))
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
		childSlot := slices.Index(job.child.attributes, a)
		parentSlot := slices.Index(job.parent.attributes, job.definition.Referenced[i])
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
		if job.parent.columns[parentSlot].Usage.Type != parentInput {
			q.WriteString("::")
			q.WriteString(pgIdentifier(parentInput.NamespaceName()))
			q.WriteByte('.')
			q.WriteString(pgIdentifier(parentInput.Name()))
		}
		q.WriteString(" OPERATOR(pg_catalog.=) ")
		q.WriteString(quoteIdentifier(job.child.columns[childSlot].Name))
		if job.child.columns[childSlot].Usage.Type != childInput {
			q.WriteString("::")
			q.WriteString(pgIdentifier(childInput.NamespaceName()))
			q.WriteByte('.')
			q.WriteString(pgIdentifier(childInput.Name()))
		}
	}
	return q.String()
}
