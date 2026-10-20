package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Reuse the shared coercion traversal; only changed execution branches are
// copied. In particular, imported-frame literals never replace analyzed nodes.
func preflightUpdate(c *exec.Context, original *boundUpdate, sourceDemand ...func(*boundUpdate) (plan.Node, error)) (*boundUpdate, error) {
	return preflightUpdateWithPlanning(c, original, true, sourceDemand...)
}

func preflightUpdateWithPlanning(c *exec.Context, original *boundUpdate, planConstants bool, sourceDemand ...func(*boundUpdate) (plan.Node, error)) (*boundUpdate, error) {
	result := original
	var assignments []expr.Expr
	targets, err := preflightArrayTargetsWithPlanning(c, original.ArrayTargets, planConstants)
	if err != nil {
		return nil, err
	}
	if len(targets) != 0 && &targets[0] != &original.ArrayTargets[0] {
		private := *original
		private.ArrayTargets = targets
		result = &private
	}
	for i, expression := range original.Assignments {
		owner := c
		var recipe exec.Context
		if len(original.DefaultOrigins) != 0 && original.DefaultOrigins[i].Requested {
			recipe = *c
			applyDefaultOrigin(original.DefaultOrigins[i], &recipe)
			owner = &recipe
		}
		folded, changed, err := preflightUpdateExpression(owner, expression, planConstants)
		if err != nil {
			return nil, err
		}
		if changed {
			if assignments == nil {
				assignments = append([]expr.Expr(nil), original.Assignments...)
			}
			assignments[i] = folded
		}
	}
	if assignments != nil {
		private := *result
		private.Assignments = assignments
		result = &private
	}
	if original.Predicate != nil {
		folded, changed, err := preflightUpdateExpression(c, original.Predicate, planConstants)
		if err != nil {
			return nil, err
		}
		if changed {
			if result == original {
				private := *original
				result = &private
			}
			result.Predicate = folded
		}
	}
	if original.From != nil {
		from := original.From
		if c.PlanStaticOperators && len(sourceDemand) != 0 {
			from, err = sourceDemand[0](result)
			if err != nil {
				return nil, err
			}
		}
		folded, err := preflightQuery(c, from)
		if err != nil {
			return nil, err
		}
		if folded != original.From {
			if result == original {
				private := *original
				result = &private
			}
			result.From = folded
		}
	}
	return result, nil
}

func preflightUpdateExpression(c *exec.Context, expression expr.Expr, planConstants bool) (expr.Expr, bool, error) {
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
func bindUpdate(b *binding, binder *expressionBinder, s *parser.Update) error {
	if s.CurrentOf != nil {
		b.CurrentOf = s.CurrentOf.Text
	}
	binder.target.relation, binder.target.ref = b.Relation, b.Ref
	binder.target.name = b.Relation.Name()
	binder.target.alias = s.Alias.Text
	binder.params.updateParameters = true
	defer func() {
		binder.params.updateParameters = false
		binder.params.updateInputParameters = nil
	}()
	u := &boundUpdate{}
	var err error
	target := binder.mutationTargetNamespace()
	binder.query.hidden = append(binder.query.hidden, target.qualifiers[0])
	if s.From != nil {
		var sources queryNamespace
		u.From, sources, err = binder.bindFrom(s.From)
		if err != nil {
			return err
		}
		u.SourceWidth = len(sources.frame)
		target, err = combineQueryNamespace(target, rebaseQueryNamespace(sources, len(b.Relation.Columns)))
		if err != nil {
			return err
		}
	}
	binder.query.namespace = &target
	binder.clause.aggregateClause = "UPDATE"
	if s.Where != nil {
		u.Predicate, err = binder.bind(s.Where, expressionPolicy{context: filterContext})
		if err != nil {
			return err
		}
	}
	if s.Returning != nil {
		b.Returning, err = binder.bindReturning(s.Returning, returningUpdate)
		if err != nil {
			return err
		}
	}
	if s.Returning != nil {
		// Only WHERE and finalized RETURNING types precede SET inference.
		// SET's own unresolved occurrences retain their contextual diagnostics.
		for number, usage := range binder.params.parameters {
			if usage.Type != nil {
				if binder.params.updateInputParameters == nil {
					binder.params.updateInputParameters = make(map[int]sqltype.Usage, len(binder.params.parameters))
				}
				binder.params.updateInputParameters[number] = usage
			}
		}
	}
	if err := bindUpdateAssignments(b, binder, s.Assignments, u); err != nil {
		return err
	}
	if s.CurrentOf != nil && b.Relation.View != nil {
		return sqlError("0A000", "WHERE CURRENT OF on a view is not implemented")
	}
	return nil
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
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return nil
}

type updateCandidate struct {
	storage.RowVersion
	slot        int
	sourceIndex int
	physical    exec.QueryPhysicalRow
}

func (e *Engine) latestUpdateRow(s *StatementScope, ref ids.ObjectRef, candidate updateCandidate) (storage.RowVersion, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return storage.RowVersion{}, false, err
	}
	if ref.Epoch != e.catalog.state.Epoch() || s.txRef.Epoch() != e.catalog.state.Epoch() {
		return storage.RowVersion{}, false, ids.ErrCatalogReplaced
	}
	store := e.privateStorageLocked(s.owner.catalog, ref.Address)
	if store == nil {
		return storage.RowVersion{}, false, ids.ErrCatalogReplaced
	}
	current := store.Versions
	hint := candidate.slot
	if candidate.Born == 0 {
		current = s.ownWrites[ref.Address]
	}
	row, _, found := rowByID(current, candidate.Row, hint)
	if !found {
		return storage.RowVersion{}, false, errors.New("stuntgres: invalid update predecessor")
	}
	if err := s.repeatableConflict(row, store.Ends); err != nil {
		return storage.RowVersion{}, false, err
	}
	return row, visibility.LiveVersion(row, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[ref.Address]), nil
}

func (e *Engine) executeUpdate(ctx context.Context, s *StatementScope, b binding, source, from *exec.Source, parameters []sqltype.Value, schema string) (Result, error) {
	defer source.Close()
	source.Host = hostOf(s)
	if b.Storage == nil || b.Storage != s.lockedStorage || b.Storage != source.View.Data().GenerationAt(b.Ref.Address) {
		if from != nil {
			from.Close()
		}
		return Result{}, ids.ErrCatalogReplaced
	}
	var positioned mutationRowIdentity
	if b.CurrentOf != "" {
		var err error
		positioned, err = s.positionedMutationRow(b)
		if err != nil {
			if from != nil {
				from.Close()
			}
			return Result{}, err
		}
	}
	inputs := source
	if from != nil {
		if err := exec.SelectQuerySources(ctx, from); err != nil {
			return Result{}, err
		}
		if b.ViewWrite != nil || b.Rule.Template {
			from = freezeViewWriteSources(s, from, b.QuerySources)
		}
		defer from.Close()
		inputs = from
	}
	state := exec.State{Source: inputs, Parameters: parameters, Input: s.input, Output: s.output, Locking: inputs.Locks != nil}
	defer state.ReleaseMemberships()
	eval := state.Evaluation(ctx)
	var smallUpdateOutputs [4]sqltype.Value
	if b.Update.OutputWidth != 0 {
		if b.Update.OutputWidth <= len(smallUpdateOutputs) {
			eval.UpdateOutputScratch = smallUpdateOutputs[:b.Update.OutputWidth]
		} else {
			eval.UpdateOutputScratch = make([]sqltype.Value, b.Update.OutputWidth)
		}
	}
	var stage exec.Stage
	var frame []sqltype.Value
	var updated map[mutationRowIdentity]bool
	sourceCount := 1
	if b.Update.From != nil {
		state.VirtualReads = exec.QueryHasVirtualReads(b.Update.From) || exec.BoundHasVirtualReads(b.Update.Predicate)
		for _, expression := range b.Update.Assignments {
			state.VirtualReads = state.VirtualReads || exec.BoundHasVirtualReads(expression)
		}
		for _, target := range b.Update.ArrayTargets {
			state.VirtualReads = state.VirtualReads || assignmentTargetHasVirtualReads(target)
		}
		if b.Returning != nil {
			for _, expression := range b.Returning.Expressions {
				state.VirtualReads = state.VirtualReads || exec.BoundHasVirtualReads(expression)
			}
		}
		var err error
		stage, err = exec.OpenNode(ctx, &state, b.Update.From)
		if err != nil {
			return Result{}, err
		}
		stage, err = exec.SettlePendingQueryMarks(ctx, &state, b.Update.From, stage)
		if err != nil {
			return Result{}, err
		}
		defer stage.Close()
		if err := stage.Drain(ctx); err != nil {
			return Result{}, err
		}
		sourceCount = stage.Count()
		if sourceCount != 0 {
			frame = make([]sqltype.Value, len(b.Relation.Columns)+b.Update.SourceWidth)
			updated = make(map[mutationRowIdentity]bool)
		}
	}
	var output *exec.FinalRows
	keep := false
	if b.Returning != nil {
		output = exec.NewFinalRows(b.Returning.Schema, 0)
		output.Output = s.output
		defer func() {
			if !keep {
				output.Close()
			}
		}()
	}
	// The candidate set is frozen before the first row wait. Rechecks can remove
	// candidates but never discover rows that did not qualify in this snapshot.
	var candidates []updateCandidate
	for i := range source.Count() {
		physical := mutationPhysicalRow(source, i, b)
		old := physical.Version
		if b.CurrentOf != "" && (positioned.ref != physical.Leaf.Ref || old.Row != positioned.row) {
			continue
		}
		for j := range sourceCount {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			eval.Row = physical.Values(old)
			eval.PhysicalTable = physical.Leaf.Ref
			if b.Update.From != nil {
				copy(frame, eval.Row)
				copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(j)))
				eval.Row = frame
				eval.BindQueryIdentity(stage.Source, stage.Index(j))
			}
			matches, err := exec.UpdateMatches(&eval, b.Update.Predicate)
			if err != nil {
				return Result{}, err
			}
			if matches {
				candidates = append(candidates, updateCandidate{RowVersion: old, slot: physical.Hint, sourceIndex: j, physical: physical})
			}
		}
	}
	type physicalBatch struct {
		ref    ids.ObjectRef
		old    []storage.RowVersion
		rows   indexWriteBatch
		staged int
		prior  map[ids.RowID]struct{}
	}
	var single physicalBatch
	single.ref = b.Ref
	if len(s.inheritedTargets) == 0 {
		capacity := min(len(candidates), source.Count())
		single.old = make([]storage.RowVersion, 0, capacity)
		single.rows.values = make([][]sqltype.Value, 0, capacity)
	}
	batches := []physicalBatch(nil)
	if len(s.inheritedTargets) != 0 && len(candidates) != 0 {
		batches = make([]physicalBatch, len(s.inheritedTargets)+1)
		batches[0].ref = b.Ref
		for i, target := range s.inheritedTargets {
			batches[i+1].ref = target.Ref
		}
		for i := range batches {
			capacity := min(len(candidates), source.Inheritance[i].Prefix.Count())
			batches[i].old = make([]storage.RowVersion, 0, capacity)
			batches[i].rows.values = make([][]sqltype.Value, 0, capacity)
		}
	}
	var count int64
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		ref := candidate.physical.Leaf.Ref
		if err := s.acquireUpdateRow(ref, candidate.Row); err != nil {
			return Result{}, err
		}
		old, live, err := e.latestUpdateRow(s, ref, candidate)
		if err != nil {
			return Result{}, err
		}
		if !live {
			continue
		}
		oldValues := candidate.physical.Values(old)
		eval.Row = oldValues
		eval.PhysicalTable = ref
		eval.OldRow = nil
		if b.Update.From != nil {
			copy(frame, oldValues)
			copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(candidate.sourceIndex)))
			eval.Row = frame
			eval.BindQueryIdentity(stage.Source, stage.Index(candidate.sourceIndex))
		}
		if old.Version != candidate.Version {
			if err := s.physicalRowEvent(ref, old, lock.MultiNoKeyLock, 0); err != nil {
				return Result{}, err
			}
			matches, err := exec.UpdateMatches(&eval, b.Update.Predicate)
			if err != nil {
				return Result{}, err
			}
			if !matches {
				continue
			}
		}
		// RHS and bounds see the unchanged old frame; partial writes compose
		// only in this private replacement. Losing FROM pairs still demand SET.
		row, err := evaluateUpdateAssignments(&eval, b.Update, b.Targets, oldValues)
		if err != nil {
			return Result{}, err
		}
		identity := mutationRowIdentity{ref, candidate.Row}
		if b.Update.From != nil && updated[identity] {
			continue
		}
		physicalRow := scatterMutationRow(candidate.physical, old, row, b.Targets)
		if err := e.validateInheritedMutationRow(s, b, candidate.physical, schema, physicalRow, &eval); err != nil {
			return Result{}, err
		}
		keyChanged, err := s.uniqueKeyChanged(ref, old.Values, physicalRow)
		if err != nil {
			return Result{}, err
		}
		if keyChanged {
			if err := s.acquireUpdateRowMode(ref, old.Row, lock.ForUpdate); err != nil {
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
		old.PendingKeyChange = old.PendingKeyChange || keyChanged
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
		if candidate.physical.Segment != nil && candidate.physical.Segment.Layout != nil {
			for i, slot := range candidate.physical.Segment.Layout {
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
			batch.prior[predecessor.Row] = struct{}{}
		}
		_, overlap := batch.prior[old.Row]
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
			if b.Update.From != nil {
				copy(frame, row)
				eval.Row = frame
			}
			for i, expr := range b.Returning.Expressions {
				value, err := expr.Eval(&eval)
				if err != nil {
					return Result{}, err
				}
				if value.Type() == nil || !value.Type().Concrete() || value.Type() != output.Columns[i].Usage.Type {
					return Result{}, errors.New("stuntgres: invalid finalized UPDATE value")
				}
				output.Values = append(output.Values, value)
			}
			output.Count++
		}
		count++
		if b.Update.From != nil {
			updated[identity] = true
		}
	}
	if output != nil && b.Returning.RegclassOutput && output.Count != 0 {
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
		return checkDetailError(schema, b.Relation.Name(), check, detail)
	}
	return notNullDetailError(schema, b.Relation.Name(), column, detail)
}

// Public UPDATE and admitted RI assignment share generation and local checks.
func (e *Engine) validateMutationRow(s *StatementScope, b binding, schema string, row []sqltype.Value, eval *exec.Context) error {
	generationEval := *eval
	if err := evaluateGenerations(eval.Context, b.Relation, row, &generationEval); err != nil {
		return err
	}
	for i, column := range b.Relation.Columns {
		if column.Nullable {
			continue
		}
		value, err := logicalColumnValue(b.Relation, i, row, &generationEval)
		if err != nil {
			return err
		}
		if value.IsNull() {
			return e.rowConstraintError(s, b, schema, column.Name, "", row)
		}
	}
	if len(b.Relation.Constraints.Check) != 0 {
		constraint, err := validateCheckRow(b.Relation, row, eval)
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
	if s != nil && s.partitionInsert != nil && b != nil && b.Relation.Address() == s.partitionInsert.target.Address() {
		row = s.partitionInsert.namedRow
	}
	return e.rowDiagnosticDetail(s, b, row, false)
}

func (e *Engine) rowDiagnosticDetail(s *StatementScope, b *binding, row []sqltype.Value, suppressEmpty bool) (string, error) {
	e.mu.Lock()
	err := s.validateLockedRelation(e)
	if err == nil && s.foreignActionAuth != nil {
		err = s.validateForeignActionLocked(e, b.Ref)
	}
	var auth AuthorizationView
	if err == nil {
		auth, err = s.writeAuthorizationLocked(e)
	}
	e.mu.Unlock()
	if err != nil {
		return "", err
	}
	whole, err := auth.Effective(b.Ref, false)
	if err != nil {
		return "", err
	}
	if whole&authorization.SelectPrivilege != 0 {
		return failingRowDetail(row, s.output, nil, nil, b.Relation)
	}
	// Preserve the small/wide supplied-column manifest convention. Assignment
	// order can differ from physical order, which governs diagnostic disclosure.
	var small uint64
	var modified []uint64
	var visibleColumns [64]int
	visible := visibleColumns[:0]
	if len(b.Relation.Columns) > 64 {
		modified = make([]uint64, (len(b.Relation.Columns)+63)/64)
		visible = make([]int, 0, len(b.Relation.Columns))
	}
	for _, index := range b.Targets {
		word := &small
		if modified != nil {
			word = &modified[index/64]
		}
		*word |= uint64(1) << uint(index%64)
	}
	for index, attribute := range b.Relation.Attributes {
		word := small
		if modified != nil {
			word = modified[index/64]
		}
		if word&(uint64(1)<<uint(index%64)) != 0 {
			visible = append(visible, index)
			continue
		}
		ref := b.Ref
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
	return failingRowDetail(row, s.output, b.Relation.Columns, visible, b.Relation)
}
