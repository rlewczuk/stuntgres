package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/extension"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (v *viewDefinition) IntaggRecipeProcedure(catalog catalog.Read, path analyze.ViewLiteralPath, name string) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := catalog.Relation(v.consumer)
	if !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !ViewReferenceValid(catalog, consumer, ref) || ref.Epoch != catalog.Epoch() && (consumer == nil || ViewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen intagg reference")
	}
	proc, ok := catalog.Procedure(ref.Address)
	if !ok || proc.Name() != name || extension.IntaggProviderOf(proc) != extension.StockIntagg {
		return nil, fmt.Errorf("stuntgres: mismatched frozen intagg procedure")
	}
	if err := extension.StockIntagg.ValidateProcedure(catalog, proc); err != nil {
		return nil, err
	}
	return proc, nil
}

func (v *viewRenderer) intaggRecipeName(catalog catalog.Read, path analyze.ViewLiteralPath, name string) (string, error) {
	proc, err := v.IntaggRecipeProcedure(catalog, path, name)
	if err != nil {
		return "", err
	}
	namespace, ok := catalog.Namespace(proc.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen intagg namespace")
	}
	return v.Presentation.identifier(namespace.Name()) + "." + v.Presentation.identifier(proc.Name()), nil
}
