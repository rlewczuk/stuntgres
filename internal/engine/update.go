package engine

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// Reuse the shared coercion traversal; only changed execution branches are
// copied. In particular, imported-frame literals never replace analyzed nodes.
func preflightUpdate(c *exec.Context, original *analyze.UpdateBinding, sourceDemand ...func(*analyze.UpdateBinding) (plan.Node, error)) (*analyze.UpdateBinding, error) {
	return preflightUpdateWithPlanning(c, original, true, sourceDemand...)
}

func preflightUpdateWithPlanning(c *exec.Context, original *analyze.UpdateBinding, planConstants bool, sourceDemand ...func(*analyze.UpdateBinding) (plan.Node, error)) (*analyze.UpdateBinding, error) {
	result := original
	var assignments []expr.Expr
	targets, err := exec.PreflightArrayTargetsWithPlanning(c, original.ArrayTargets, planConstants)
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
			analyze.ApplyDefaultOrigin(original.DefaultOrigins[i], &recipe)
			owner = &recipe
		}
		folded, changed, err := exec.PreflightUpdateExpression(owner, expression, planConstants)
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
		folded, changed, err := exec.PreflightUpdateExpression(c, original.Predicate, planConstants)
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
		folded, err := view.PreflightQuery(c, from)
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

// SQL waits never retain replacement admission or state/manager mutexes.
func (s *StatementScope) acquireUpdateRowMode(ref ids.ObjectRef, row ids.RowID, mode lock.Mode) error {
	e := s.engine()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
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

// executeUpdate runs the dml UPDATE executor over the scope's host.
func executeUpdate(ctx context.Context, s *StatementScope, b analyze.Binding, source, from *exec.Source, parameters []sqltype.Value, schema string) (Result, error) {
	outcome, err := dml.ExecuteUpdate(s.writeHost(), dml.UpdateInput{Context: ctx, Binding: b, Source: source, From: from, Parameters: parameters, Schema: schema})
	if err != nil {
		return Result{}, err
	}
	return outcomeResult(outcome), nil
}

// Capture only immutable authority under metadata locks. Provider output runs
// unlocked; disclosure uses the named target's supplied-column manifest.
func (e *Engine) rowConstraintDetail(s *StatementScope, b *analyze.Binding, row []sqltype.Value) (string, error) {
	if s != nil && s.PartitionInsert != nil && b != nil && b.Relation.Address() == s.PartitionInsert.Target.Address() {
		row = s.PartitionInsert.NamedRow
	}
	return e.rowDiagnosticDetail(s, b, row, false)
}

func (e *Engine) rowDiagnosticDetail(s *StatementScope, b *analyze.Binding, row []sqltype.Value, suppressEmpty bool) (string, error) {
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
		return exec.FailingRowDetail(row, s.output, nil, nil, b.Relation)
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
	return exec.FailingRowDetail(row, s.output, b.Relation.Columns, visible, b.Relation)
}
