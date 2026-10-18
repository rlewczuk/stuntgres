package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type boundUpdate struct {
	assignments  []expr.Expr
	arrayTargets []*arrayAssignmentTarget
	predicate    expr.Expr
	from         LogicalNode
	sourceWidth  int
}

// Reuse the shared coercion traversal; only changed execution branches are
// copied. In particular, imported-frame literals never replace analyzed nodes.
func preflightUpdate(c *EvalContext, original *boundUpdate, sourceDemand ...func(*boundUpdate) (LogicalNode, error)) (*boundUpdate, error) {
	return preflightUpdateWithPlanning(c, original, true, sourceDemand...)
}

func preflightUpdateWithPlanning(c *EvalContext, original *boundUpdate, planConstants bool, sourceDemand ...func(*boundUpdate) (LogicalNode, error)) (*boundUpdate, error) {
	result := original
	var assignments []expr.Expr
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
				assignments = append([]expr.Expr(nil), original.assignments...)
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
		from := original.from
		if c.planStaticOperators && len(sourceDemand) != 0 {
			from, err = sourceDemand[0](result)
			if err != nil {
				return nil, err
			}
		}
		folded, err := preflightQuery(c, from)
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

func preflightUpdateExpression(c *EvalContext, expression expr.Expr, planConstants bool) (expr.Expr, bool, error) {
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
	binder.targetName = b.relation.Name()
	binder.targetAlias = s.Alias.Text
	binder.updateParameters = true
	defer func() {
		binder.updateParameters = false
		binder.updateInputParameters = nil
	}()
	u := &boundUpdate{assignments: make([]expr.Expr, len(s.Assignments))}
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
		target, err = combineQueryNamespace(target, rebaseQueryNamespace(sources, len(b.relation.Columns)))
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
	if s.CurrentOf != nil && b.relation.View != nil {
		return sqlError("0A000", "WHERE CURRENT OF on a view is not implemented")
	}
	return nil
}

func updateMatches(eval *EvalContext, predicate expr.Expr) (bool, error) {
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
func (s *StatementScope) acquireUpdateRow(ref ids.ObjectRef, row ids.RowID) error {
	return s.acquireUpdateRowMode(ref, row, lock.ForNoKeyUpdate)
}
func (s *StatementScope) acquireUpdateRowMode(ref ids.ObjectRef, row ids.RowID, mode lock.Mode) error {
	e := s.op.session.Engine()
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref, Row: row}, mode, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	if s.implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.epoch {
		return ids.ErrCatalogReplaced
	}
	return nil
}

type updateCandidate struct {
	rowVersion
	slot        int
	sourceIndex int
	physical    queryPhysicalRow
}

func (e *Engine) latestUpdateRow(s *StatementScope, ref ids.ObjectRef, candidate updateCandidate) (rowVersion, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return rowVersion{}, false, err
	}
	if ref.Epoch != e.catalog.state.epoch || s.txRef.Epoch() != e.catalog.state.epoch {
		return rowVersion{}, false, ids.ErrCatalogReplaced
	}
	store := e.privateStorageLocked(s.owner.catalog, ref.Address)
	if store == nil {
		return rowVersion{}, false, ids.ErrCatalogReplaced
	}
	current := store.versions
	hint := candidate.slot
	if candidate.born == 0 {
		current = s.ownWrites[ref.Address]
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
		return Result{}, ids.ErrCatalogReplaced
	}
	var positioned mutationRowIdentity
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
		if b.viewWrite != nil || b.ruleTemplate {
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
	var updated map[mutationRowIdentity]bool
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
			frame = make([]sqltype.Value, len(b.relation.Columns)+b.update.sourceWidth)
			updated = make(map[mutationRowIdentity]bool)
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
		physical := mutationPhysicalRow(source, i, b)
		old := physical.version
		if b.currentOf != "" && (positioned.ref != physical.leaf.ref || old.row != positioned.row) {
			continue
		}
		for j := range sourceCount {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			eval.Row = physical.values(old)
			eval.physicalTable = physical.leaf.ref
			if b.update.from != nil {
				copy(frame, eval.Row)
				copy(frame[len(b.relation.Columns):], stage.source.row(stage.index(j)))
				eval.Row = frame
				eval.bindQueryIdentity(stage.source, stage.index(j))
			}
			matches, err := updateMatches(&eval, b.update.predicate)
			if err != nil {
				return Result{}, err
			}
			if matches {
				candidates = append(candidates, updateCandidate{rowVersion: old, slot: physical.hint, sourceIndex: j, physical: physical})
			}
		}
	}
	type physicalBatch struct {
		ref    ids.ObjectRef
		old    []rowVersion
		rows   indexWriteBatch
		staged int
		prior  map[ids.RowID]struct{}
	}
	var single physicalBatch
	single.ref = b.ref
	if len(s.inheritedTargets) == 0 {
		capacity := min(len(candidates), source.count())
		single.old = make([]rowVersion, 0, capacity)
		single.rows.values = make([][]sqltype.Value, 0, capacity)
	}
	batches := []physicalBatch(nil)
	if len(s.inheritedTargets) != 0 && len(candidates) != 0 {
		batches = make([]physicalBatch, len(s.inheritedTargets)+1)
		batches[0].ref = b.ref
		for i, target := range s.inheritedTargets {
			batches[i+1].ref = target.ref
		}
		for i := range batches {
			capacity := min(len(candidates), source.inheritance[i].prefix.count())
			batches[i].old = make([]rowVersion, 0, capacity)
			batches[i].rows.values = make([][]sqltype.Value, 0, capacity)
		}
	}
	var count int64
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		ref := candidate.physical.leaf.ref
		if err := s.acquireUpdateRow(ref, candidate.row); err != nil {
			return Result{}, err
		}
		old, live, err := e.latestUpdateRow(s, ref, candidate)
		if err != nil {
			return Result{}, err
		}
		if !live {
			continue
		}
		oldValues := candidate.physical.values(old)
		eval.Row = oldValues
		eval.physicalTable = ref
		eval.OldRow = nil
		if b.update.from != nil {
			copy(frame, oldValues)
			copy(frame[len(b.relation.Columns):], stage.source.row(stage.index(candidate.sourceIndex)))
			eval.Row = frame
			eval.bindQueryIdentity(stage.source, stage.index(candidate.sourceIndex))
		}
		if old.version != candidate.version {
			if err := s.physicalRowEvent(ref, old, lock.MultiNoKeyLock, 0); err != nil {
				return Result{}, err
			}
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
		row, err := evaluateUpdateAssignments(&eval, b.update, b.targets, oldValues)
		if err != nil {
			return Result{}, err
		}
		identity := mutationRowIdentity{ref, candidate.row}
		if b.update.from != nil && updated[identity] {
			continue
		}
		physicalRow := scatterMutationRow(candidate.physical, old, row, b.targets)
		if err := e.validateInheritedMutationRow(s, b, candidate.physical, schema, physicalRow, &eval); err != nil {
			return Result{}, err
		}
		keyChanged, err := s.uniqueKeyChanged(ref, old.values, physicalRow)
		if err != nil {
			return Result{}, err
		}
		if keyChanged {
			if err := s.acquireUpdateRowMode(ref, old.row, lock.ForUpdate); err != nil {
				return Result{}, err
			}
			if _, live, err := e.latestUpdateRow(s, ref, candidate); err != nil {
				return Result{}, err
			} else if !live {
				continue
			}
		}
		if err := s.claimUniqueRow(ref, old); err != nil {
			return Result{}, err
		}
		old.pendingKeyChange = old.pendingKeyChange || keyChanged
		batch := &single
		for i := range batches {
			if batches[i].ref == ref {
				batch = &batches[i]
				break
			}
		}
		batch.old = append(batch.old, old)
		actionMode := lock.MultiNoKeyAction
		if keyChanged {
			actionMode = lock.MultiUpdateAction
		}
		if err := s.physicalRowEvent(ref, old, actionMode, 0); err != nil {
			return Result{}, err
		}
		proposal := indexWriteCandidate{values: physicalRow}
		if err := s.checkUniqueRow(ref, &proposal, batch.old, batch.rows); err != nil {
			return Result{}, err
		}
		if err := e.checkViewRow(s, &b, row, &eval); err != nil {
			return Result{}, err
		}
		if candidate.physical.segment != nil && candidate.physical.segment.layout != nil {
			for i, slot := range candidate.physical.segment.layout {
				row[i] = physicalRow[slot]
			}
		}
		batch.rows.append(proposal)
		one := batch.rows.slice(len(batch.old)-1, len(batch.old))
		one.actionMode = actionMode
		// Other action producers can have staged this physical target already.
		// Index only their prefix/additions; our accepted targets are distinct
		// and appending never mutates a prefix retained by an earlier view.
		existing := s.stage.rows[ref.Address]
		if len(existing) < batch.staged {
			batch.staged = 0
		}
		for _, predecessor := range existing[batch.staged:] {
			if batch.prior == nil {
				batch.prior = make(map[ids.RowID]struct{})
			}
			batch.prior[predecessor.row] = struct{}{}
		}
		_, overlap := batch.prior[old.row]
		if overlap {
			err = s.Writes().replace(ref, batch.old[len(batch.old)-1:], one)
		} else {
			err = s.Writes().appendUpdateReplacement(ref, batch.old[len(batch.old)-1:], one)
		}
		if err != nil {
			return Result{}, err
		}
		batch.staged = len(s.stage.rows[ref.Address])
		if output != nil {
			eval.Row, eval.OldRow = row, oldValues
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
		count++
		if b.update.from != nil {
			updated[identity] = true
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
	keep = true
	if output != nil {
		return Result{Count: count, Rows: output}, nil
	}
	return Result{Count: count}, nil
}

func (e *Engine) rowConstraintError(s *StatementScope, b binding, schema, column, check string, row []sqltype.Value) error {
	detail, err := e.rowConstraintDetail(s, &b, row)
	if err != nil {
		return err
	}
	if check != "" {
		return checkDetailError(schema, b.relation.Name(), check, detail)
	}
	return notNullDetailError(schema, b.relation.Name(), column, detail)
}

// Public UPDATE and admitted RI assignment share generation and local checks.
func (e *Engine) validateMutationRow(s *StatementScope, b binding, schema string, row []sqltype.Value, eval *EvalContext) error {
	generationEval := *eval
	if err := evaluateGenerations(eval.Context, b.relation, row, &generationEval); err != nil {
		return err
	}
	for i, column := range b.relation.Columns {
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
	if len(b.relation.CheckConstraints) != 0 {
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
	if s != nil && s.partitionInsert != nil && b != nil && b.relation.Address() == s.partitionInsert.target.Address() {
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
	if len(b.relation.Columns) > 64 {
		modified = make([]uint64, (len(b.relation.Columns)+63)/64)
		visible = make([]int, 0, len(b.relation.Columns))
	}
	for _, index := range b.targets {
		word := &small
		if modified != nil {
			word = &modified[index/64]
		}
		*word |= uint64(1) << uint(index%64)
	}
	for index, attribute := range b.relation.Attributes {
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
	return failingRowDetail(row, s.output, b.relation.Columns, visible, b.relation)
}
