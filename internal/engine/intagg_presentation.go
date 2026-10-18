package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Occurrences use the same normalized structural paths as frozen literal and
// column presentation. The captured reference comes from binding, never a later
// lookup by the written name or the renderer's current search_path.
func (b *expressionBinder) captureIntaggReference(path viewLiteralPath, proc *procedureDescriptor, ref ids.ObjectRef, name string) error {
	if err := validateIntaggIdentity(b.bindingCatalog(), proc, ref, name); err != nil {
		return err
	}
	if b.viewIntaggRefs == nil {
		b.viewIntaggRefs = make(map[viewLiteralPath]ids.ObjectRef)
	}
	if old, exists := b.viewIntaggRefs[path]; exists && old != ref {
		return fmt.Errorf("stuntgres: conflicting frozen intagg occurrence")
	}
	b.viewIntaggRefs[path] = ref
	return nil
}

func intaggRecipeFunction(raw parser.Expr) string {
	switch call := raw.(type) {
	case parser.AggregateExpr:
		if call.Name == "int_array_aggregate" {
			return call.Name
		}
	case parser.FunctionExpr:
		if len(call.Name.Parts) != 0 && call.Name.Parts[len(call.Name.Parts)-1].Text == "int_array_enum" {
			return "int_array_enum"
		}
	}
	return ""
}

func (v *viewDefinition) intaggRecipeProcedure(catalog CatalogRead, path viewLiteralPath, name string) (*procedureDescriptor, error) {
	ref, exists := v.intaggRefs[path]
	consumer, _ := catalog.state.descriptors[v.consumer].(*relationDescriptor)
	if !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !storedViewReferenceValid(catalog, consumer, ref) || ref.Epoch != catalog.Epoch() && (consumer == nil || consumer.view != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen intagg reference")
	}
	proc, ok := catalog.state.descriptors[ref.Address].(*procedureDescriptor)
	if !ok || proc.name != name || proc.intagg != stockIntagg {
		return nil, fmt.Errorf("stuntgres: mismatched frozen intagg procedure")
	}
	if err := stockIntagg.validateProcedure(catalog, proc); err != nil {
		return nil, err
	}
	return proc, nil
}

func (v *viewRenderer) intaggRecipeName(catalog CatalogRead, path viewLiteralPath, name string) (string, error) {
	proc, err := v.intaggRecipeProcedure(catalog, path, name)
	if err != nil {
		return "", err
	}
	namespace, ok := catalog.state.descriptors[proc.namespace].(*namespaceDescriptor)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen intagg namespace")
	}
	return v.presentation.identifier(namespace.name) + "." + v.presentation.identifier(proc.name), nil
}
