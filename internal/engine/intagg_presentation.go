package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Occurrences use the same normalized structural paths as frozen literal and
// column presentation. The captured reference comes from binding, never a later
// lookup by the written name or the renderer's current search_path.
func (b *expressionBinder) captureIntaggReference(path viewLiteralPath, proc *catalog.Procedure, ref ids.ObjectRef, name string) error {
	if err := validateIntaggIdentity(b.bindingCatalog(), proc, ref, name); err != nil {
		return err
	}
	return b.capture.captureRoutineReference(path, ref)
}

func (v *viewDefinition) intaggRecipeProcedure(catalog catalog.Read, path viewLiteralPath, name string) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := catalog.Relation(v.consumer)
	if !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !storedViewReferenceValid(catalog, consumer, ref) || ref.Epoch != catalog.Epoch() && (consumer == nil || viewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen intagg reference")
	}
	proc, ok := catalog.Procedure(ref.Address)
	if !ok || proc.Name() != name || intaggProviderOf(proc) != stockIntagg {
		return nil, fmt.Errorf("stuntgres: mismatched frozen intagg procedure")
	}
	if err := stockIntagg.validateProcedure(catalog, proc); err != nil {
		return nil, err
	}
	return proc, nil
}

func (v *viewRenderer) intaggRecipeName(catalog catalog.Read, path viewLiteralPath, name string) (string, error) {
	proc, err := v.intaggRecipeProcedure(catalog, path, name)
	if err != nil {
		return "", err
	}
	namespace, ok := catalog.Namespace(proc.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen intagg namespace")
	}
	return v.presentation.identifier(namespace.Name()) + "." + v.presentation.identifier(proc.Name()), nil
}
