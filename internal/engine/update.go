package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type boundUpdate struct {
	assignments []BoundExpr
	predicate   BoundExpr
	from        LogicalNode
	sourceWidth int
}

// Reuse the shared coercion traversal; only changed execution branches are
// copied. In particular, imported-frame literals never replace analyzed nodes.
func preflightUpdate(c *EvalContext, original *boundUpdate) (*boundUpdate, error) {
	result := original
	var assignments []BoundExpr
	for i, expression := range original.assignments {
		folded, changed, err := preflightCoercions(c, expression)
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
		private := *original
		private.assignments = assignments
		result = &private
	}
	if original.predicate != nil {
		folded, changed, err := preflightCoercions(c, original.predicate)
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

// Transform FROM, WHERE, RETURNING (output fallback), then RHS/destinations.
func (e *Engine) bindUpdate(b *binding, binder *expressionBinder, s *parser.Update) error {
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
	target := binder.updateTargetNamespace()
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
		b.returning, err = binder.bindReturning(s.Returning, true)
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
	for i, a := range s.Assignments {
		if slot, exists := b.relation.index[a.Target.Text]; exists {
			if _, generated := b.relation.generations[b.relation.attributes[slot]]; generated {
				if _, requested := a.Value.(parser.DefaultExpr); !requested {
					return generationWriteError(a.Target.Text, true)
				}
				continue
			}
		}
		if _, requested := a.Value.(parser.DefaultExpr); requested {
			return expressionError("0A000", "UPDATE DEFAULT is unsupported", a.Value.SourcePosition())
		}
		expected := sqltype.Usage{}
		if slot, ok := b.relation.index[a.Target.Text]; ok {
			expected = b.relation.columns[slot].Usage
		}
		u.assignments[i], err = binder.bind(a.Value, expressionPolicy{context: projectionContext, expected: expected})
		if err != nil {
			return err
		}
	}
	seen := make(map[int]bool, len(s.Assignments))
	for i, a := range s.Assignments {
		slot, ok := b.relation.index[a.Target.Text]
		if !ok {
			switch a.Target.Text {
			case "ctid", "tableoid", "xmin", "xmax", "cmin", "cmax":
				return expressionError("0A000", fmt.Sprintf(`cannot assign to system column "%s"`, a.Target.Text), a.Target.Pos)
			}
			diagnostic := expressionError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Target.Text, b.relation.name), a.Target.Pos).(*sqlstate.Error)
			visible := binder.targetName
			if binder.targetAlias != "" {
				visible = binder.targetAlias
			}
			if len(a.Indirection) != 0 && a.Target.Text == visible {
				diagnostic.Hint = "SET target columns cannot be qualified with the relation name."
			}
			return diagnostic
		}
		if len(a.Indirection) != 0 {
			return expressionError("0A000", "field assignments are unsupported", a.Target.Pos)
		}
		if seen[slot] {
			return sqlError("42601", fmt.Sprintf(`multiple assignments to same column "%s"`, a.Target.Text))
		}
		seen[slot] = true
		if _, generated := b.relation.generations[b.relation.attributes[slot]]; generated {
			b.targets = append(b.targets, slot)
			continue
		}
		if identity, ok := b.relation.identities[b.relation.attributes[slot]]; ok && identity.mode == parser.IdentityAlways {
			diagnostic := identityInsertError(a.Target.Text).(*sqlstate.Error)
			diagnostic.Message = fmt.Sprintf(`column "%s" can only be updated to DEFAULT`, a.Target.Text)
			diagnostic.Hint = ""
			return diagnostic
		}
		b.targets = append(b.targets, slot)
		source := u.assignments[i]
		dest := b.relation.columns[slot]
		if source.ResultUsage() != dest.Usage {
			plan, err := sqltype.PlanCoercion(source.ResultUsage(), dest.Usage, sqltype.Assignment)
			if err != nil {
				var d *sqlstate.Error
				if errors.As(err, &d) && d.Code == "42804" {
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, dest.Name, dest.Usage.Type.DiagnosticName(), source.ResultUsage().Type.DiagnosticName()), Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(parser.ExpressionStart(a.Value).Character)}
				}
				return err
			}
			source = coercionExpression{binder.info(dest.Usage, a.Value.SourcePosition(), coercionProperties(source, plan)), source, plan, sqltype.Assignment}
			u.assignments[i] = source
		}
	}
	b.update = u
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
	policy := relationLockPolicy{timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
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
	inputs := source
	if from != nil {
		defer from.close()
		if err := selectQuerySources(ctx, from); err != nil {
			return Result{}, err
		}
		inputs = from
	}
	state := queryState{source: inputs, parameters: parameters, input: s.input, output: s.output, locking: inputs.locks != nil}
	eval := state.evaluation(ctx)
	var stage indexedStage
	var frame, assigned []sqltype.Value
	var updated map[RowID]bool
	sourceCount := 1
	if b.update.from != nil {
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
			assigned = make([]sqltype.Value, len(b.targets))
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
		for j := range sourceCount {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			eval.Row = old.values
			if b.update.from != nil {
				copy(frame, old.values)
				copy(frame[len(old.values):], stage.source.row(stage.index(j)))
				eval.Row = frame
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
	rows := make([][]sqltype.Value, 0, uniqueCapacity)
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
		if b.update.from != nil {
			// PostgreSQL projects SET for losing duplicate pairs too. Suppress
			// only replacement/count/RETURNING, never eligible scalar demand.
			for i, expr := range b.update.assignments {
				if expr == nil {
					continue
				}
				value, err := expr.Eval(&eval)
				if err != nil {
					return Result{}, err
				}
				assigned[i] = value
			}
			if updated[candidate.row] {
				continue
			}
		}
		row := make([]sqltype.Value, len(old.values))
		copy(row, old.values)
		for i, expr := range b.update.assignments {
			if expr == nil {
				continue
			}
			if b.update.from != nil {
				row[b.targets[i]] = assigned[i]
			} else {
				value, err := expr.Eval(&eval)
				if err != nil {
					return Result{}, err
				}
				row[b.targets[i]] = value
			}
		}
		generationEval := eval
		if err := evaluateGenerations(ctx, b.relation, row, &generationEval); err != nil {
			return Result{}, err
		}
		for i, column := range b.relation.columns {
			if !column.Nullable && row[i].IsNull() {
				return Result{}, e.rowConstraintError(s, b, schema, column.Name, "", row)
			}
		}
		if len(b.relation.checkConstraints) != 0 {
			constraint, err := validateCheckRow(b.relation, row, &eval)
			if err != nil {
				return Result{}, err
			}
			if constraint != nil {
				return Result{}, e.rowConstraintError(s, b, schema, "", constraint.Name, row)
			}
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
		if err := s.claimUniqueRow(b.ref, old.values); err != nil {
			return Result{}, err
		}
		old.pendingKeyChange = old.pendingKeyChange || keyChanged
		oldRows = append(oldRows, old)
		if err := s.checkUniqueRow(b.ref, row, oldRows, rows); err != nil {
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
		rows = append(rows, row)
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
		return Result{Count: int64(len(rows)), Rows: output}, nil
	}
	return Result{Count: int64(len(rows))}, nil
}

// Only the immutable authority snapshot is captured under metadata locks.
// Provider output runs after unlocking, with the completed simultaneous row.
func (e *Engine) rowConstraintError(s *StatementScope, b binding, schema, column, check string, row []sqltype.Value) error {
	e.mu.Lock()
	err := s.validateLockedRelation(e)
	var auth AuthorizationView
	if err == nil {
		auth, err = e.authorizationLocked(s.op)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	whole, err := auth.Effective(b.ref, false)
	if err != nil {
		return err
	}
	if whole&SelectPrivilege != 0 {
		if check == "" {
			return notNullError(schema, b.relation.name, column, row, s.output)
		}
		detail, err := failingRowDetail(row, s.output, nil, nil)
		if err != nil {
			return err
		}
		return checkDetailError(schema, b.relation.name, check, detail)
	}
	// Target order is assignment order; disclosure order is physical order.
	// Match the existing small/wide stable-column manifest convention.
	var small uint64
	var modified []uint64
	if len(b.relation.columns) > 64 {
		modified = make([]uint64, (len(b.relation.columns)+63)/64)
	}
	for _, index := range b.targets {
		word := &small
		if modified != nil {
			word = &modified[index/64]
		}
		*word |= uint64(1) << uint(index%64)
	}
	visible := make([]int, 0, len(b.relation.columns))
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
			return err
		}
		if rights&SelectPrivilege != 0 {
			visible = append(visible, index)
		}
	}
	detail, err := failingRowDetail(row, s.output, b.relation.columns, visible)
	if err != nil {
		return err
	}
	if check != "" {
		return checkDetailError(schema, b.relation.name, check, detail)
	}
	return notNullDetailError(schema, b.relation.name, column, detail)
}
