package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (v *viewDefinition) SQLRoutineRecipeProcedure(read catalog.Read, path analyze.ViewLiteralPath, raw parser.FunctionExpr) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := read.Relation(v.consumer)
	if !raw.StoredSQLRoutine || !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !ViewReferenceValid(read, consumer, ref) || ref.Epoch != read.Epoch() && (consumer == nil || ViewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen SQL routine reference")
	}
	d, ok := read.Procedure(ref.Address)
	if !ok || d.SQL == nil || len(raw.Name.Parts) != 2 || raw.Name.Parts[1].Text != d.Name() || len(raw.Args) != len(d.SQL.Inputs) {
		return nil, fmt.Errorf("stuntgres: mismatched frozen SQL routine procedure")
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(read, d); err != nil {
		return nil, err
	}
	for _, dependency := range v.dependencies {
		if dependency.Ref == ref && dependency.Revision != 0 {
			return d, nil
		}
	}
	return nil, fmt.Errorf("stuntgres: missing frozen SQL routine dependency")
}

func (v *viewRenderer) sqlRoutineRecipeName(read catalog.Read, path analyze.ViewLiteralPath, raw parser.FunctionExpr) (string, error) {
	d, err := v.SQLRoutineRecipeProcedure(read, path, raw)
	if err != nil {
		return "", err
	}
	namespace, ok := read.Namespace(d.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen SQL routine namespace")
	}
	return v.Presentation.identifier(namespace.Name()) + "." + v.Presentation.identifier(d.Name()), nil
}

func sameMappedSQLRoutine(current, captured *catalog.Procedure, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	if current.SQL == nil || captured.SQL == nil || current.SQL.Source != captured.SQL.Source || len(current.SQL.Inputs) != len(captured.SQL.Inputs) {
		return false
	}
	result := typ(captured.SQL.Result.Type)
	if result == nil || result.ID() != current.SQL.Result.Type.ID() {
		return false
	}
	for i, input := range captured.SQL.Inputs {
		mapped := typ(input.Type)
		if mapped == nil || mapped.ID() != current.SQL.Inputs[i].Type.ID() {
			return false
		}
	}
	return true
}
