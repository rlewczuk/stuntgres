package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
)

type declarationCaptureFrame struct {
	procedure *catalog.Procedure
	parent    *declarationCaptureFrame
}

// captureNativeDeclarationBody records source identity/lifetime only. The real
// entered wrapper initializes executable occurrences later under its subject.
func (b *Binder) captureNativeDeclarationBody(outer *catalog.Procedure) error {
	return b.captureNativeDeclarationCalls(outer, nil)
}
func (b *Binder) captureNativeDeclarationCalls(outer *catalog.Procedure, parent *declarationCaptureFrame) error {
	if outer == nil || outer.DeclarationBody == nil || len(outer.Body) != 0 {
		return nil
	}
	for frame := parent; frame != nil; frame = frame.parent {
		if frame.procedure == outer {
			return fmt.Errorf("stuntgres: cyclic native declaration body")
		}
	}
	recipe, ok := proc.RoutineDeclarationRecipe(outer.DeclarationBody)
	if !ok {
		return fmt.Errorf("stuntgres: missing original native declaration recipe")
	}
	frame := declarationCaptureFrame{procedure: outer, parent: parent}
	view := b.BindingCatalog()
	for ordinal := range recipe.CallCount() {
		_, part := recipe.CallPart(ordinal)
		selected, err := exec.DeclarationBodyRoutine(view, outer, part)
		if err != nil {
			return err
		}
		ref := view.Owner().Domain.Ref(view.Epoch(), selected.Address())
		b.dependency(ref, selected.Revision())
		if len(selected.Body) != 0 {
			if err = b.captureRoutineBody(selected); err != nil {
				return err
			}
		} else if err = b.captureNativeDeclarationCalls(selected, &frame); err != nil {
			return err
		}
	}
	return nil
}
