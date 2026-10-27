package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ValidateMetadata checks the immutable checked owner's internal structure and
// capture provenance. Current executable admission is deliberately separate:
// replacing a callee does not replace this routine's retained declaration.
func (p *RoutineProgram) ValidateMetadata(view catalog.Read) error {
	if p == nil || len(p.actionDependencies) != len(p.actions) {
		return fmt.Errorf("stuntgres: incomplete routine metadata capture")
	}
	if err := validateRoutineMetadataDependencies(view, p.dependencies); err != nil {
		return err
	}
	if err := p.validateDependencySet(); err != nil {
		return err
	}
	for i, action := range p.actions {
		if action.syntax == nil || action.presentation == nil {
			return fmt.Errorf("stuntgres: incomplete routine command capture")
		}
		if err := validateRoutineActionShape(action); err != nil {
			return err
		}
		// Empty reads select the existing retained-original validation path,
		// used by remapping before destination admission. No live callee lookup.
		if err := validateRoutineScalars(action.presentation, catalog.Read{}); err != nil {
			return err
		}
		if err := validateRoutineActionScalars(action, catalog.Read{}); err != nil {
			return err
		}
		if err := validateRoutineModifiers(action, catalog.Read{}); err != nil {
			return err
		}
		if err := validateRoutineCapture(action, p.actionDependencies[i], catalog.Read{}); err != nil {
			return err
		}
	}
	return nil
}

// ValidateMetadata does not reconstruct defaults against current declarations.
// Definition association checks the input layout/usages separately; these
// checks retain the original manifest and domain/epoch without re-admission.
func (p *RoutineDefaults) ValidateMetadata(view catalog.Read) error {
	if p == nil || view.Owner() == nil || p.domain != view.Owner().Domain || p.epoch != view.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if err := validateRoutineMetadataDependencies(view, p.dependencies); err != nil {
		return err
	}
	for _, dependency := range p.dependencies {
		if dependency.Purpose != catalog.DefinitionDependency && dependency.Purpose != catalog.OperandDependency {
			return fmt.Errorf("stuntgres: invalid routine metadata default purpose")
		}
	}
	for _, node := range p.entries {
		if node != nil && !node.ResultUsage().Valid() {
			return fmt.Errorf("stuntgres: invalid routine metadata default usage")
		}
	}
	return nil
}

func validateRoutineMetadataDependencies(view catalog.Read, dependencies []catalog.ExpressionDependency) error {
	if view.Owner() == nil || view.Owner().Domain == nil {
		return ids.ErrCatalogReplaced
	}
	for _, dependency := range dependencies {
		if dependency.Ref.Domain() != view.Owner().Domain || dependency.Ref.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if !dependency.Purpose.Valid() || dependency.Revision == 0 {
			return fmt.Errorf("stuntgres: invalid routine metadata dependency")
		}
	}
	return nil
}

// routineMetadataProcedure resolves presentation labels through logical object
// identity, while preserving the retained owner's principal provenance. It
// neither admits execution nor compares mutable implementation/attribute state.
func routineMetadataProcedure(view catalog.Read, captured *catalog.Procedure) (*catalog.Procedure, error) {
	if captured == nil || view.Owner() == nil || view.Owner().Principals == nil {
		return nil, fmt.Errorf("stuntgres: missing routine metadata owner")
	}
	if _, ok := view.Database(view.Owner().DatabaseAddress()); !ok || captured.Owner.ID() == 0 || captured.Owner != view.Owner().Principals.Principal(captured.Owner.ID()) {
		return nil, fmt.Errorf("stuntgres: foreign routine metadata principal")
	}
	current, ok := view.Procedure(captured.Address())
	if !ok || current.Address() != captured.Address() || current.Signature != captured.Signature || current.RoutineKind != captured.RoutineKind || current.Result != captured.Result {
		return nil, fmt.Errorf("stuntgres: mismatched routine metadata identity")
	}
	return current, nil
}
