package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Reuse the persisted recipe's actual reference when validating it. COW ACL and
// owner changes do not replace lifetime identity; native retagging remaps both
// the descriptor carrier and existing manifest before this renderer runs.
func (r scalarRenderer) recordStoredRoutine(view catalog.Read, node expr.Expr) error {
	if selected, ok := node.(plan.SelectedOperatorExpr); ok {
		// The wrapper owns operator identity, not a second invocation. Its
		// actual provider owns the selected routine and immutable body.
		return r.recordStoredRoutine(view, selected.Provider)
	}
	if expression, ok := node.(plan.CaseExpr); ok && expression.Test != nil {
		for _, arm := range expression.Arms {
			if arm.Routine != nil {
				if err := r.recordStoredRoutineDescriptor(view, arm.Routine); err != nil {
					return err
				}
			}
		}
	}
	captured, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil {
		return err
	}
	if err := r.recordStoredRoutineDescriptor(view, captured); err != nil {
		return err
	}
	return exec.WalkRoutineBody(captured, node, func(body *catalog.Procedure) error {
		current, ok := r.descriptor(view, body.Address()).(*catalog.Procedure)
		if !ok || !exec.SameRoutineDeclaration(current, body) {
			return fmt.Errorf("stuntgres: stale stored SQL body declaration")
		}
		// Body links describe immutable declarations; only this renderer's
		// original captured manifest can supply the remapped owning lifetime.
		return r.recordStoredRoutineDescriptor(view, current)
	})
}

func (r scalarRenderer) recordStoredRoutineDescriptor(view catalog.Read, captured *catalog.Procedure) error {
	current, err := exec.ValidateStoredRoutineDescriptor(view, captured)
	if err != nil {
		return err
	}
	ref := view.Owner().Domain.Ref(view.Epoch(), current.Address())
	for _, old := range r.CapturedManifest {
		if old.Address == ref.Address {
			if !ReferenceEpochValid(view, r.retainedConsumer(), old) {
				return fmt.Errorf("stuntgres: stale stored routine reference")
			}
			r.record(old)
			return nil
		}
	}
	for _, old := range r.CapturedChecks {
		if old.Ref.Address == ref.Address {
			if !ReferenceEpochValid(view, r.retainedConsumer(), old.Ref) || current.Revision() != old.Revision {
				return fmt.Errorf("stuntgres: stale stored routine dependency")
			}
			r.record(old.Ref)
			return nil
		}
	}
	r.record(ref)
	return nil
}
