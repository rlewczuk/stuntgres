package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
)

// Both installed and SQL routines use the existing occurrence identity map.
// Syntax markers demand a capture, but never grant catalog authority themselves.
func (b *expressionBinder) captureRoutineReference(path viewLiteralPath, ref ids.ObjectRef) error {
	if b.viewRoutineRefs == nil {
		b.viewRoutineRefs = make(map[viewLiteralPath]ids.ObjectRef)
	}
	if old, exists := b.viewRoutineRefs[path]; exists && old != ref {
		return fmt.Errorf("stuntgres: conflicting frozen routine occurrence")
	}
	b.viewRoutineRefs[path] = ref
	return nil
}

func (b *expressionBinder) captureSQLRoutineReference(path viewLiteralPath, n sqlRoutineExpression) error {
	if err := n.ValidateStoredProvider(); err != nil {
		return err
	}
	read := b.bindingCatalog()
	if n.ref != b.engine.catalog.Domain.Ref(read.Epoch(), n.procedure.Address()) {
		return fmt.Errorf("stuntgres: invalid frozen SQL routine reference")
	}
	if _, err := routine.ValidateStoredRoutineDescriptor(read, n.procedure); err != nil {
		return err
	}
	return b.captureRoutineReference(path, n.ref)
}

func (v *viewDefinition) sqlRoutineRecipeProcedure(read catalog.Read, path viewLiteralPath, raw parser.FunctionExpr) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := read.Relation(v.consumer)
	if !raw.StoredSQLRoutine || !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !storedViewReferenceValid(read, consumer, ref) || ref.Epoch != read.Epoch() && (consumer == nil || viewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen SQL routine reference")
	}
	d, ok := read.Procedure(ref.Address)
	if !ok || d.SQL == nil || len(raw.Name.Parts) != 2 || raw.Name.Parts[1].Text != d.Name() || len(raw.Args) != len(d.SQL.Inputs) {
		return nil, fmt.Errorf("stuntgres: mismatched frozen SQL routine procedure")
	}
	if _, err := routine.ValidateStoredRoutineDescriptor(read, d); err != nil {
		return nil, err
	}
	for _, dependency := range v.dependencies {
		if dependency.Ref == ref && dependency.Revision != 0 {
			return d, nil
		}
	}
	return nil, fmt.Errorf("stuntgres: missing frozen SQL routine dependency")
}

func (v *viewRenderer) sqlRoutineRecipeName(read catalog.Read, path viewLiteralPath, raw parser.FunctionExpr) (string, error) {
	d, err := v.sqlRoutineRecipeProcedure(read, path, raw)
	if err != nil {
		return "", err
	}
	namespace, ok := read.Namespace(d.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen SQL routine namespace")
	}
	return v.presentation.identifier(namespace.Name()) + "." + v.presentation.identifier(d.Name()), nil
}
