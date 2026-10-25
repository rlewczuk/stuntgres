package stored

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (v *View) SQLRoutineRecipeProcedure(read catalog.Read, path analyze.ViewLiteralPath, raw parser.FunctionExpr) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := read.Relation(v.consumer)
	if !raw.StoredSQLRoutine || !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !ViewReferenceValid(read, consumer, ref) || ref.Epoch != read.Epoch() && (consumer == nil || ViewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen SQL routine reference")
	}
	d, ok := read.Procedure(ref.Address)
	if !ok || d.SQL == nil && d.RangeConstructor == nil || len(raw.Name.Parts) != 2 {
		return nil, fmt.Errorf("stuntgres: mismatched frozen SQL routine procedure")
	}
	if _, valid := analyze.SQLCallableShape(read, nil, d, raw); !valid {
		return nil, fmt.Errorf("stuntgres: mismatched frozen SQL routine call shape")
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(read, d); err != nil {
		return nil, err
	}
	for _, dependency := range v.dependencies {
		if dependency.Ref == ref && dependency.Revision != 0 && dependency.Purpose == catalog.DefinitionDependency {
			return d, nil
		}
	}
	return nil, fmt.Errorf("stuntgres: missing frozen SQL routine dependency")
}

func (v *QueryRenderer) sqlRoutineRecipeName(read catalog.Read, path analyze.ViewLiteralPath, raw parser.FunctionExpr) (string, error) {
	d, err := v.SQLRoutineRecipeProcedure(read, path, raw)
	if err != nil {
		return "", err
	}
	if v.Presentation.Deparse {
		return (Deparser{View: read, Path: v.Presentation.Path, Output: v.Presentation.Output}).sqlRoutineName(d)
	}
	namespace, ok := read.Namespace(d.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen SQL routine namespace")
	}
	return v.Presentation.identifier(namespace.Name()) + "." + v.Presentation.identifier(d.Name()), nil
}

func (r Deparser) sqlRoutineName(d *catalog.Procedure) (string, error) {
	namespace, ok := r.View.Namespace(d.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing SQL routine namespace")
	}
	selected, count := regobj.SelectExactRoutine(r.View, analyze.NamespaceSelection{Path: r.Path}, d.Name(), d.Signature)
	name := r.Identifier(d.Name())
	if count == 1 && selected.ID == d.Address().ObjectID {
		return name, nil
	}
	return r.Identifier(namespace.Name()) + "." + name, nil
}

func (v *View) AggregateRecipeProcedure(read catalog.Read, path analyze.ViewLiteralPath, raw parser.AggregateExpr) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := read.Relation(v.consumer)
	if !raw.StoredAggregate || !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !ViewReferenceValid(read, consumer, ref) || ref.Epoch != read.Epoch() && (consumer == nil || ViewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen aggregate reference")
	}
	d, ok := read.Procedure(ref.Address)
	if !ok || !d.DynamicCallable() || d.Aggregate == nil || raw.Schema == nil {
		return nil, fmt.Errorf("stuntgres: mismatched frozen aggregate procedure")
	}
	if raw.Within || raw.Variadic || raw.JSON != nil || raw.Star != (len(raw.Args) == 0) || raw.Window != nil && (raw.Distinct || len(raw.Order) != 0 || raw.Filter != nil) {
		return nil, fmt.Errorf("stuntgres: mismatched frozen aggregate call shape")
	}
	if _, valid := analyze.SQLCallableShape(read, nil, d, parser.FunctionExpr{Args: raw.Args}); !valid {
		return nil, fmt.Errorf("stuntgres: mismatched frozen aggregate call shape")
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(read, d); err != nil {
		return nil, err
	}
	if err := routine.ValidateAggregateDefinition(read, d); err != nil {
		return nil, err
	}
	for _, dependency := range v.dependencies {
		if dependency.Ref == ref && dependency.Revision != 0 && dependency.Purpose == catalog.DefinitionDependency {
			return d, nil
		}
	}
	return nil, fmt.Errorf("stuntgres: missing frozen aggregate dependency")
}

func (v *QueryRenderer) aggregateRecipeName(read catalog.Read, path analyze.ViewLiteralPath, raw parser.AggregateExpr) (string, error) {
	d, err := v.AggregateRecipeProcedure(read, path, raw)
	if err != nil {
		return "", err
	}
	namespace, ok := read.Namespace(d.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing frozen aggregate namespace")
	}
	return v.Presentation.identifier(namespace.Name()) + "." + v.Presentation.identifier(d.Name()), nil
}

func sameMappedSQLRoutine(current, captured *catalog.Procedure, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation func(sqltype.CollationRef) (sqltype.CollationRef, error)) (bool, error) {
	if current.SQL == nil || captured.SQL == nil || current.Display != captured.Display || current.SQL.Kind != captured.SQL.Kind || current.PLHandler != captured.PLHandler || current.SQL.Standard != captured.SQL.Standard || current.SQL.Source != captured.SQL.Source || !slices.Equal(current.SQL.Names, captured.SQL.Names) || len(current.SQL.Inputs) != len(captured.SQL.Inputs) {
		return false, nil
	}
	if captured.SQL.Standard && !slices.Equal(current.SQL.Statements, captured.SQL.Statements) {
		return false, nil
	}
	result := typ(captured.SQL.Result.Type)
	if result == nil || result.ID() != current.SQL.Result.Type.ID() {
		return false, nil
	}
	for i, input := range captured.SQL.Inputs {
		mapped := typ(input.Type)
		if mapped == nil || mapped.ID() != current.SQL.Inputs[i].Type.ID() {
			return false, nil
		}
	}
	if len(current.SQL.Formals) != len(captured.SQL.Formals) {
		return false, nil
	}
	for i, formal := range captured.SQL.Formals {
		next := current.SQL.Formals[i]
		mapped := typ(formal.Usage.Type)
		if mapped == nil || mapped.ID() != next.Usage.Type.ID() || formal.Mode != next.Mode || formal.Name != next.Name || formal.HasDefault != next.HasDefault {
			return false, nil
		}
	}
	// Replay-only prerequisites may receive new object OIDs during replacement.
	// Executable identity is checked through the fully mapped lexical Usages.
	if len(current.DeclarationDependencies) != len(captured.DeclarationDependencies) {
		return false, nil
	}
	for i, source := range captured.DeclarationDependencies {
		next := current.DeclarationDependencies[i]
		if source.ClassID != next.ClassID || source.SubID != next.SubID {
			return false, nil
		}
	}
	if (current.SQL.PL == nil) != (captured.SQL.PL == nil) {
		return false, nil
	}
	if captured.SQL.PL != nil {
		if !captured.SQL.PL.SameLayout(current.SQL.PL) {
			return false, nil
		}
		if len(current.SQL.PL.Variables) != len(captured.SQL.PL.Variables) {
			return false, nil
		}
		for i, variable := range captured.SQL.PL.Variables {
			next := current.SQL.PL.Variables[i]
			if variable.Name != next.Name || variable.Record != next.Record || variable.Constant != next.Constant || variable.NotNull != next.NotNull || variable.InheritInputCollation != next.InheritInputCollation {
				return false, nil
			}
			if !variable.Record {
				mapped := variable.Usage
				mapped.Type = typ(mapped.Type)
				if mapped.Type == nil {
					return false, nil
				}
				if collation != nil && mapped.Collation.ID != 0 {
					var err error
					mapped.Collation, err = collation(mapped.Collation)
					if err != nil {
						return false, err
					}
				}
				if mapped != next.Usage {
					return false, nil
				}
			}
		}
	}
	return true, nil
}
