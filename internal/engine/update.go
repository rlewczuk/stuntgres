package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type boundUpdate struct {
	assignments  []BoundExpr
	arrayTargets []*arrayAssignmentTarget
	predicate    BoundExpr
	from         LogicalNode
	sourceWidth  int
}

// Reuse the shared coercion traversal; only changed execution branches are
// copied. In particular, imported-frame literals never replace analyzed nodes.
func preflightUpdate(c *EvalContext, original *boundUpdate) (*boundUpdate, error) {
	return preflightUpdateWithPlanning(c, original, true)
}

func preflightUpdateWithPlanning(c *EvalContext, original *boundUpdate, planConstants bool) (*boundUpdate, error) {
	result := original
	var assignments []BoundExpr
	targets, err := preflightArrayTargetsWithPlanning(c, original.arrayTargets, planConstants)
	if err != nil {
		return nil, err
	}
	if len(targets) != 0 && &targets[0] != &original.arrayTargets[0] {
		private := *original
		private.arrayTargets = targets
		result = &private
	}
	for i, expression := range original.assignments {
		folded, changed, err := preflightUpdateExpression(c, expression, planConstants)
		if err != nil {
			return nil, err
		}
		if changed {
			if assignments == nil {
				assignments = append([]BoundExpr(nil), original.assignments...)
			}
			assignments[i] = folded
		}
	}
	if assignments != nil {
		private := *result
		private.assignments = assignments
		result = &private
	}
	if original.predicate != nil {
		folded, changed, err := preflightUpdateExpression(c, original.predicate, planConstants)
		if err != nil {
			return nil, err
		}
		if changed {
			if result == original {
				private := *original
				result = &private
			}
			result.predicate = folded
		}
	}
	if original.from != nil {
		folded, err := preflightQuery(c, original.from)
		if err != nil {
			return nil, err
		}
		if folded != original.from {
			if result == original {
				private := *original
				result = &private
			}
			result.from = folded
		}
	}
	return result, nil
}

func preflightUpdateExpression(c *EvalContext, expression BoundExpr, planConstants bool) (BoundExpr, bool, error) {
	changed := false
	if planConstants {
		// Plan SQL constants before importing execution-frame coercions:
		// imported parameter literals must not become SQL constants.
		next, did, err := simplifyConditional(c, expression, false)
		if err != nil {
			return nil, false, err
		}
		expression, changed = next, did
	}
	next, did, err := preflightCoercions(c, expression)
	return next, changed || did, err
}

// Transform FROM, WHERE, RETURNING (output fallback), then RHS/destinations.
func (e *Engine) bindUpdate(b *binding, binder *expressionBinder, s *parser.Update) error {
	if s.CurrentOf != nil {
		b.currentOf = s.CurrentOf.Text
	}
	binder.relation, binder.ref = b.relation, b.ref
	binder.targetName = b.relation.name
	binder.targetAlias = s.Alias.Text
	binder.updateParameters = true
	defer func() {
		binder.updateParameters = false
		binder.updateInputParameters = nil
	}()
	u := &boundUpdate{assignments: make([]BoundExpr, len(s.Assignments))}
	var err error
	target := binder.mutationTargetNamespace()
	binder.queryHidden = append(binder.queryHidden, target.qualifiers[0])
	if s.From != nil {
		var sources queryNamespace
		u.from, sources, err = binder.bindFrom(s.From)
		if err != nil {
			return err
		}
		u.sourceWidth = len(sources.frame)
		target, err = combineQueryNamespace(target, rebaseQueryNamespace(sources, len(b.relation.columns)))
		if err != nil {
			return err
		}
	}
	binder.queryNamespace = &target
	binder.aggregateClause = "UPDATE"
	if s.Where != nil {
		u.predicate, err = binder.bind(s.Where, expressionPolicy{context: filterContext})
		if err != nil {
			return err
		}
	}
	if s.Returning != nil {
		b.returning, err = binder.bindReturning(s.Returning, returningUpdate)
		if err != nil {
			return err
		}
	}
	if s.Returning != nil {
		// Only WHERE and finalized RETURNING types precede SET inference.
		// SET's own unresolved occurrences retain their contextual diagnostics.
		for number, usage := range binder.parameters {
			if usage.Type != nil {
				if binder.updateInputParameters == nil {
					binder.updateInputParameters = make(map[int]sqltype.Usage, len(binder.parameters))
				}
				binder.updateInputParameters[number] = usage
			}
		}
	}
	if err := e.bindUpdateAssignments(b, binder, s.Assignments, u); err != nil {
		return err
	}
	if s.CurrentOf != nil && b.relation.view != nil {
		return sqlError("0A000", "WHERE CURRENT OF on a view is not implemented")
	}
	return nil
}

func updateMatches(eval *EvalContext, predicate BoundExpr) (bool, error) {
	if predicate == nil {
		return true, eval.Context.Err()
	}
	value, err := predicate.Eval(eval)
	if err != nil {
		return false, err
	}
	truth, err := TruthOf(value)
	return truth == True, err
}

// SQL waits never retain replacement admission or state/manager mutexes.
func (s *StatementScope) acquireUpdateRow(ref ObjectRef, row RowID) error {
	return s.acquireUpdateRowMode(ref, row, noKeyUpdate)
}
func (s *StatementScope) acquireUpdateRowMode(ref ObjectRef, row RowID, mode relationLockMode) error {
	e := s.op.session.Engine()
	policy := relationLockPolicy{operation: s.op, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref, row: row}, mode, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
	}
	if err != nil {
		return err
	}
	if s.implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	return nil
}

type updateCandidate struct {
	rowVersion
	slot        int
	sourceIndex int
}

func (e *Engine) latestUpdateRow(s *StatementScope, ref ObjectRef, candidate updateCandidate) (rowVersion, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return rowVersion{}, false, err
	}
	if ref.Epoch != e.state.epoch || s.txRef.epoch != e.state.epoch {
		return rowVersion{}, false, ErrCatalogReplaced
	}
	store := e.privateStorageLocked(s.owner.catalog, ref.Address)
	if store == nil {
		return rowVersion{}, false, ErrCatalogReplaced
	}
	current := store.versions
	hint := candidate.slot
	if candidate.born == 0 {
		current = s.ownWrites[ref.Address]
		hint -= len(s.view.read().committed[ref.Address])
	}
	row, _, found := rowByID(current, candidate.row, hint)
	if !found {
		return rowVersion{}, false, errors.New("stuntgres: invalid update predecessor")
	}
	if err := s.repeatableConflict(row, store.ends); err != nil {
		return rowVersion{}, false, err
	}
	return row, liveVersion(row, e.commitSeq, store.ends, s.ownDeletes[ref.Address]), nil
}

func (e *Engine) executeUpdate(ctx context.Context, s *StatementScope, b binding, source, from *querySource, parameters []sqltype.Value, schema string) (Result, error) {
	defer source.close()
	source.scope = s
	if b.storage == nil || b.storage != s.lockedStorage || b.storage != source.view.read().generationAt(b.ref.Address) {
		if from != nil {
			from.close()
		}
		return Result{}, ErrCatalogReplaced
	}
	var positioned RowID
	if b.currentOf != "" {
		var err error
		positioned, err = s.positionedMutationRow(b)
		if err != nil {
			if from != nil {
				from.close()
			}
			return Result{}, err
		}
	}
	inputs := source
	if from != nil {
		if err := selectQuerySources(ctx, from); err != nil {
			return Result{}, err
		}
		if b.viewWrite != nil {
			from = freezeViewWriteSources(s, from, b.querySources)
		}
		defer from.close()
		inputs = from
	}
	state := queryState{source: inputs, parameters: parameters, input: s.input, output: s.output, locking: inputs.locks != nil}
	defer state.releaseMemberships()
	eval := state.evaluation(ctx)
	var stage indexedStage
	var frame []sqltype.Value
	var updated map[RowID]bool
	sourceCount := 1
	if b.update.from != nil {
		state.virtualReads = queryHasVirtualReads(b.update.from) || boundHasVirtualReads(b.update.predicate)
		for _, expression := range b.update.assignments {
			state.virtualReads = state.virtualReads || boundHasVirtualReads(expression)
		}
		for _, target := range b.update.arrayTargets {
			state.virtualReads = state.virtualReads || assignmentTargetHasVirtualReads(target)
		}
		if b.returning != nil {
			for _, expression := range b.returning.expressions {
				state.virtualReads = state.virtualReads || boundHasVirtualReads(expression)
			}
		}
		var err error
		stage, err = openQueryNode(ctx, &state, b.update.from)
		if err != nil {
			return Result{}, err
		}
		stage, err = settlePendingQueryMarks(ctx, &state, b.update.from, stage)
		if err != nil {
			return Result{}, err
		}
		defer stage.Close()
		if err := stage.drain(ctx); err != nil {
			return Result{}, err
		}
		sourceCount = stage.count()
		if sourceCount != 0 {
			frame = make([]sqltype.Value, len(b.relation.columns)+b.update.sourceWidth)
			updated = make(map[RowID]bool)
		}
	}
	var output *finalRows
	keep := false
	if b.returning != nil {
		output = newFinalRows(b.returning.schema, 0)
		output.output = s.output
		defer func() {
			if !keep {
				output.Close()
			}
		}()
	}
	// The candidate set is frozen before the first row wait. Rechecks can remove
	// candidates but never discover rows that did not qualify in this snapshot.
	var candidates []updateCandidate
	for i := range source.count() {
		old := source.versionAt(source.rawIndex(i))
		if b.currentOf != "" && (positioned == 0 || old.row != positioned) {
			continue
		}
		for j := range sourceCount {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			eval.Row = old.values
			if b.update.from != nil {
				copy(frame, old.values)
				copy(frame[len(old.values):], stage.source.row(stage.index(j)))
				eval.Row = frame
				eval.bindQueryIdentity(stage.source, stage.index(j))
			}
			matches, err := updateMatches(&eval, b.update.predicate)
			if err != nil {
				return Result{}, err
			}
			if matches {
				candidates = append(candidates, updateCandidate{rowVersion: old, slot: source.rawIndex(i), sourceIndex: j})
			}
		}
	}
	uniqueCapacity := min(len(candidates), source.count())
	oldRows := make([]rowVersion, 0, uniqueCapacity)
	rows := indexWriteBatch{values: make([][]sqltype.Value, 0, uniqueCapacity)}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if err := s.acquireUpdateRow(b.ref, candidate.row); err != nil {
			return Result{}, err
		}
		old, live, err := e.latestUpdateRow(s, b.ref, candidate)
		if err != nil {
			return Result{}, err
		}
		if !live {
			continue
		}
		eval.Row = old.values
		eval.OldRow = nil
		if b.update.from != nil {
			copy(frame, old.values)
			copy(frame[len(old.values):], stage.source.row(stage.index(candidate.sourceIndex)))
			eval.Row = frame
			eval.bindQueryIdentity(stage.source, stage.index(candidate.sourceIndex))
		}
		if old.version != candidate.version {
			matches, err := updateMatches(&eval, b.update.predicate)
			if err != nil {
				return Result{}, err
			}
			if !matches {
				continue
			}
		}
		// RHS and bounds see the unchanged old frame; partial writes compose
		// only in this private replacement. Losing FROM pairs still demand SET.
		row, err := evaluateUpdateAssignments(&eval, b.update, b.targets, old.values)
		if err != nil {
			return Result{}, err
		}
		if b.update.from != nil && updated[candidate.row] {
			continue
		}
		if err := e.validateMutationRow(s, b, schema, row, &eval); err != nil {
			return Result{}, err
		}
		keyChanged, err := s.uniqueKeyChanged(b.ref, old.values, row)
		if err != nil {
			return Result{}, err
		}
		if keyChanged {
			if err := s.acquireUpdateRowMode(b.ref, old.row, rowUpdate); err != nil {
				return Result{}, err
			}
			if _, live, err := e.latestUpdateRow(s, b.ref, candidate); err != nil {
				return Result{}, err
			} else if !live {
				continue
			}
		}
		if err := s.claimUniqueRow(b.ref, old); err != nil {
			return Result{}, err
		}
		old.pendingKeyChange = old.pendingKeyChange || keyChanged
		oldRows = append(oldRows, old)
		proposal := indexWriteCandidate{values: row}
		if err := s.checkUniqueRow(b.ref, &proposal, oldRows, rows); err != nil {
			return Result{}, err
		}
		if err := e.checkViewRow(s, &b, row, &eval); err != nil {
			return Result{}, err
		}
		if output != nil {
			eval.Row, eval.OldRow = row, old.values
			if b.update.from != nil {
				copy(frame, row)
				eval.Row = frame
			}
			for i, expr := range b.returning.expressions {
				value, err := expr.Eval(&eval)
				if err != nil {
					return Result{}, err
				}
				if value.Type() == nil || !value.Type().Concrete() || value.Type() != output.schema[i].Usage.Type {
					return Result{}, errors.New("stuntgres: invalid finalized UPDATE value")
				}
				output.values = append(output.values, value)
			}
			output.count++
		}
		rows.append(proposal)
		if b.update.from != nil {
			updated[candidate.row] = true
		}
	}
	if output != nil && b.returning.regclassOutput && output.count != 0 {
		if err := e.captureRegclassOutput(s.op, output); err != nil {
			return Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := s.Writes().replace(b.ref, oldRows, rows); err != nil {
		return Result{}, err
	}
	keep = true
	if output != nil {
		return Result{Count: int64(len(rows.values)), Rows: output}, nil
	}
	return Result{Count: int64(len(rows.values))}, nil
}

func (e *Engine) rowConstraintError(s *StatementScope, b binding, schema, column, check string, row []sqltype.Value) error {
	detail, err := e.rowConstraintDetail(s, &b, row)
	if err != nil {
		return err
	}
	if check != "" {
		return checkDetailError(schema, b.relation.name, check, detail)
	}
	return notNullDetailError(schema, b.relation.name, column, detail)
}

// Public UPDATE and admitted RI assignment share generation and local checks.
func (e *Engine) validateMutationRow(s *StatementScope, b binding, schema string, row []sqltype.Value, eval *EvalContext) error {
	generationEval := *eval
	if err := evaluateGenerations(eval.Context, b.relation, row, &generationEval); err != nil {
		return err
	}
	for i, column := range b.relation.columns {
		if column.Nullable {
			continue
		}
		value, err := logicalColumnValue(b.relation, i, row, &generationEval)
		if err != nil {
			return err
		}
		if value.IsNull() {
			return e.rowConstraintError(s, b, schema, column.Name, "", row)
		}
	}
	if len(b.relation.checkConstraints) != 0 {
		constraint, err := validateCheckRow(b.relation, row, eval)
		if err != nil {
			return err
		}
		if constraint != nil {
			return e.rowConstraintError(s, b, schema, "", constraint.Name, row)
		}
	}
	return nil
}

// Capture only immutable authority under metadata locks. Provider output runs
// unlocked; disclosure uses the named target's supplied-column manifest.
func (e *Engine) rowConstraintDetail(s *StatementScope, b *binding, row []sqltype.Value) (string, error) {
	if s != nil && s.partitionInsert != nil && b != nil && b.relation.address == s.partitionInsert.target.address {
		row = s.partitionInsert.namedRow
	}
	return e.rowDiagnosticDetail(s, b, row, false)
}

func (e *Engine) rowDiagnosticDetail(s *StatementScope, b *binding, row []sqltype.Value, suppressEmpty bool) (string, error) {
	e.mu.Lock()
	err := s.validateLockedRelation(e)
	if err == nil && s.foreignActionAuth != nil {
		err = s.validateForeignActionLocked(e, b.ref)
	}
	var auth AuthorizationView
	if err == nil {
		auth, err = s.writeAuthorizationLocked(e)
	}
	e.mu.Unlock()
	if err != nil {
		return "", err
	}
	whole, err := auth.Effective(b.ref, false)
	if err != nil {
		return "", err
	}
	if whole&authorization.SelectPrivilege != 0 {
		return failingRowDetail(row, s.output, nil, nil, b.relation)
	}
	// Preserve the small/wide supplied-column manifest convention. Assignment
	// order can differ from physical order, which governs diagnostic disclosure.
	var small uint64
	var modified []uint64
	var visibleColumns [64]int
	visible := visibleColumns[:0]
	if len(b.relation.columns) > 64 {
		modified = make([]uint64, (len(b.relation.columns)+63)/64)
		visible = make([]int, 0, len(b.relation.columns))
	}
	for _, index := range b.targets {
		word := &small
		if modified != nil {
			word = &modified[index/64]
		}
		*word |= uint64(1) << uint(index%64)
	}
	for index, attribute := range b.relation.attributes {
		word := small
		if modified != nil {
			word = modified[index/64]
		}
		if word&(uint64(1)<<uint(index%64)) != 0 {
			visible = append(visible, index)
			continue
		}
		ref := b.ref
		ref.Address.SubID = attribute
		rights, err := auth.Effective(ref, false)
		if err != nil {
			return "", err
		}
		if rights&authorization.SelectPrivilege != 0 {
			visible = append(visible, index)
		}
	}
	if suppressEmpty && len(visible) == 0 {
		return "", nil
	}
	return failingRowDetail(row, s.output, b.relation.columns, visible, b.relation)
}
