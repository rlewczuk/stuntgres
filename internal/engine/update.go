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
	return result, nil
}

// Transform WHERE and all RHS nodes before destination assignment checks.
func (e *Engine) bindUpdate(b *binding, binder *expressionBinder, s *parser.Update) error {
	binder.relation, binder.ref = b.relation, b.ref
	binder.updateParameters = true
	defer func() { binder.updateParameters = false }()
	u := &boundUpdate{assignments: make([]BoundExpr, len(s.Assignments))}
	var err error
	if s.Where != nil {
		u.predicate, err = binder.bind(s.Where, expressionPolicy{context: filterContext})
		if err != nil {
			return err
		}
	}
	for i, a := range s.Assignments {
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
			return expressionError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Target.Text, b.relation.name), a.Target.Pos)
		}
		if seen[slot] {
			return sqlError("42601", fmt.Sprintf(`multiple assignments to same column "%s"`, a.Target.Text))
		}
		seen[slot] = true
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
			source = coercionExpression{binder.info(dest.Usage, a.Value.SourcePosition(), source.Properties()), source, plan, sqltype.Assignment}
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
	e := s.op.session.Engine()
	policy := relationLockPolicy{timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref, row: row}, noKeyUpdate, policy)
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
	slot int
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
	store := e.storage[ref.Address]
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

func (e *Engine) executeUpdate(ctx context.Context, s *StatementScope, b binding, source *querySource, parameters []sqltype.Value, schema string) (Result, error) {
	defer source.close()
	eval := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: parameters, sequenceScope: s}
	// The candidate set is frozen before the first row wait. Rechecks can remove
	// candidates but never discover rows that did not qualify in this snapshot.
	var candidates []updateCandidate
	for i := range source.count() {
		old := source.versionAt(source.rawIndex(i))
		eval.Row = old.values
		matches, err := updateMatches(&eval, b.update.predicate)
		if err != nil {
			return Result{}, err
		}
		if matches {
			candidates = append(candidates, updateCandidate{rowVersion: old, slot: source.rawIndex(i)})
		}
	}
	oldRows := make([]rowVersion, 0, len(candidates))
	rows := make([][]sqltype.Value, 0, len(candidates))
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
		if old.version != candidate.version {
			matches, err := updateMatches(&eval, b.update.predicate)
			if err != nil {
				return Result{}, err
			}
			if !matches {
				continue
			}
		}
		row := make([]sqltype.Value, len(old.values))
		copy(row, old.values)
		for i, expr := range b.update.assignments {
			value, err := expr.Eval(&eval)
			if err != nil {
				return Result{}, err
			}
			row[b.targets[i]] = value
		}
		for i, column := range b.relation.columns {
			if !column.Nullable && row[i].IsNull() {
				return Result{}, e.updateNotNullError(s, b, schema, column.Name, row)
			}
		}
		oldRows = append(oldRows, old)
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := s.Writes().replace(b.ref, oldRows, rows); err != nil {
		return Result{}, err
	}
	return Result{Count: int64(len(rows))}, nil
}

// Only the immutable authority snapshot is captured under metadata locks.
// Provider output runs after unlocking, with the completed simultaneous row.
func (e *Engine) updateNotNullError(s *StatementScope, b binding, schema, column string, row []sqltype.Value) error {
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
		return notNullError(schema, b.relation.name, column, row, s.output)
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
	return notNullDetailError(schema, b.relation.name, column, detail)
}
