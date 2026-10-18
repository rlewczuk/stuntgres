package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

// Reuse the persisted recipe's actual reference when validating it. COW ACL and
// owner changes do not replace lifetime identity; native retagging remaps both
// the descriptor carrier and existing manifest before this renderer runs.
func (r scalarRenderer) recordStoredRoutine(view CatalogRead, node expr.Expr) error {
	if selected, ok := node.(selectedOperatorExpression); ok {
		// The wrapper owns operator identity, not a second invocation. Its
		// actual provider owns the selected routine and immutable body.
		return r.recordStoredRoutine(view, selected.provider)
	}
	if expression, ok := node.(caseExpression); ok && expression.test != nil {
		for _, arm := range expression.arms {
			if arm.routine != nil {
				if err := r.recordStoredRoutineDescriptor(view, arm.routine); err != nil {
					return err
				}
			}
		}
	}
	captured, err := expressionRoutineDescriptor(node)
	if err != nil || captured == nil {
		return err
	}
	if err := r.recordStoredRoutineDescriptor(view, captured); err != nil {
		return err
	}
	return walkRoutineBody(captured, node, func(body *procedureDescriptor) error {
		current, ok := r.descriptor(view, body.Address()).(*procedureDescriptor)
		if !ok || !sameRoutineDeclaration(current, body) {
			return fmt.Errorf("stuntgres: stale stored SQL body declaration")
		}
		// Body links describe immutable declarations; only this renderer's
		// original captured manifest can supply the remapped owning lifetime.
		return r.recordStoredRoutineDescriptor(view, current)
	})
}

func (r scalarRenderer) recordStoredRoutineDescriptor(view CatalogRead, captured *procedureDescriptor) error {
	current, err := validateStoredRoutineDescriptor(view, captured)
	if err != nil {
		return err
	}
	ref := view.owner.Domain.Ref(view.Epoch(), current.Address())
	for _, old := range r.capturedManifest {
		if old.Address == ref.Address {
			if !storedReferenceEpochValid(view, r.retainedConsumer(), old) {
				return fmt.Errorf("stuntgres: stale stored routine reference")
			}
			r.record(old)
			return nil
		}
	}
	for _, old := range r.capturedChecks {
		if old.Ref.Address == ref.Address {
			if !storedReferenceEpochValid(view, r.retainedConsumer(), old.Ref) || current.Revision() != old.Revision {
				return fmt.Errorf("stuntgres: stale stored routine dependency")
			}
			r.record(old.Ref)
			return nil
		}
	}
	r.record(ref)
	return nil
}
