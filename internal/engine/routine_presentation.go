package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
)

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
