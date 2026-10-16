package engine

import "fmt"

// Reuse the persisted recipe's actual reference when validating it. COW ACL and
// owner changes do not replace lifetime identity; native retagging remaps both
// the descriptor carrier and existing manifest before this renderer runs.
func (r scalarRenderer) recordStoredRoutine(view CatalogRead, node BoundExpr) error {
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
		current, ok := r.descriptor(view, body.address).(*procedureDescriptor)
		if !ok || !sameRoutineDeclaration(current, body) {
			return fmt.Errorf("stuntgres: stale stored SQL body declaration")
		}
		// Body links describe immutable declarations; only this renderer's
		// original captured manifest can supply the remapped owning lifetime.
		return r.recordStoredRoutineDescriptor(view, current)
	})
}

func (r scalarRenderer) recordStoredRoutineDescriptor(view CatalogRead, captured *procedureDescriptor) error {
	roles := view.owner.roleRead(view, view.owner.roles)
	current, err := validateRoutineDescriptor(view, roles, captured)
	if err != nil {
		return err
	}
	ref := ObjectRef{owner: view.owner.owner, Epoch: view.Epoch(), Address: current.address}
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
		if old.ref.Address == ref.Address {
			if !storedReferenceEpochValid(view, r.retainedConsumer(), old.ref) || current.revision != old.revision {
				return fmt.Errorf("stuntgres: stale stored routine dependency")
			}
			r.record(old.ref)
			return nil
		}
	}
	r.record(ref)
	return nil
}
