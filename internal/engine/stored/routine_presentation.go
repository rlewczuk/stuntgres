package stored

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
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
	if !ok || d.RoutineDefinition() == nil && d.RangeConstructor == nil || len(raw.Name.Parts) != 2 {
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
	if v.Presentation.RoutineDeclaration {
		ref, ok := v.routineRefs[path]
		if !ok || !raw.StoredSQLRoutine || ref.Domain() != read.Owner().Domain || ref.Epoch != read.Epoch() {
			return "", ids.ErrCatalogReplaced
		}
		d, ok := read.Procedure(ref.Address)
		if !ok {
			return "", ids.ErrCatalogReplaced
		}
		return (Deparser{View: read, Path: v.Presentation.Path, Output: v.Presentation.Output, RoutineDeclaration: true}).sqlRoutineName(d)
	}
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
	return r.routineIdentityName(catalog.RoutineIdentity{ID: d.Address().ObjectID, Namespace: d.Namespace().ObjectID, Name: d.Name(), Signature: d.Signature})
}

func (r Deparser) routineIdentityName(d catalog.RoutineIdentity) (string, error) {
	namespace, ok := r.View.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: d.Namespace})
	if !ok {
		return "", fmt.Errorf("stuntgres: missing SQL routine namespace")
	}
	selected, count := regobj.SelectExactRoutine(r.View, analyze.NamespaceSelection{Path: r.Path}, d.Name, d.Signature)
	name := r.Identifier(d.Name)
	if count == 1 && selected.ID == d.ID {
		return name, nil
	}
	return r.Identifier(namespace.Name()) + "." + name, nil
}

// VariadicRoutineName presents a known variadic identity using the actual
// argument types. Expanded formals are presentation metadata, not an executable
// declaration or permission to call a lookup-only builtin.
func (r Deparser) VariadicRoutineName(ctx context.Context, identity catalog.RoutineIdentity, fixed []sqltype.Usage, element sqltype.Usage, args []expr.Expr) (string, error) {
	if len(args) < len(fixed) || element.Type == nil {
		return "", fmt.Errorf("stuntgres: invalid variadic routine presentation")
	}
	namespace, ok := r.View.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: identity.Namespace})
	if !ok {
		return "", fmt.Errorf("stuntgres: missing SQL routine namespace")
	}
	path := analyze.NamespaceSelection{Path: r.Path}
	if r.Path == nil {
		path.Qualified = namespace.Address()
	}
	qualified := r.Identifier(namespace.Name()) + "." + r.Identifier(identity.Name)
	selected, count := regobj.SelectExactRoutine(r.View, path, identity.Name, identity.Signature)
	if count != 1 || selected.ID != identity.ID {
		return qualified, nil
	}
	formals := make([]sqltype.Usage, len(args))
	copy(formals, fixed)
	for i := len(fixed); i < len(formals); i++ {
		formals[i] = element
	}
	operands := make([]analyze.ComparisonOperand, len(args))
	for i, arg := range args {
		operands[i] = analyze.ComparisonOperand{Node: arg}
	}
	raw := parser.FunctionExpr{Args: make([]parser.Expr, len(args))}
	var candidates []analyze.SQLCallable
	for i := range path.Count() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		ns := path.At(i)
		current, exists := r.View.Namespace(ns)
		if !exists || current.Temporary != (ids.SessionToken{}) {
			continue
		}
		if ns.ObjectID == identity.Namespace {
			candidates = analyze.AppendSQLCallable(candidates, analyze.SQLCallable{Inputs: formals, Variadic: true, Path: i})
		}
		for d := range catalog.DefinitionProceduresNamedAt(r.View, nil, ns, identity.Name) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if d.Address().ObjectID == identity.ID {
				continue
			}
			candidate, valid := analyze.SQLCallableShape(r.View, nil, d, raw)
			if valid {
				candidate.Path = i
				candidates = analyze.AppendSQLCallable(candidates, candidate)
			}
		}
	}
	candidates = analyze.SelectSQLCallables(r.View, candidates, operands, nil)
	if len(candidates) == 1 && candidates[0].Procedure == nil {
		return r.Identifier(identity.Name), nil
	}
	return qualified, nil
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
	ordered := d.Aggregate.Definition.Kind != "n"
	if raw.Within != ordered || raw.Variadic || raw.JSON != nil {
		return nil, fmt.Errorf("stuntgres: mismatched frozen aggregate call shape")
	}
	arguments := raw.Args
	if ordered {
		if raw.Window != nil || raw.Distinct || raw.Star && len(raw.Args) != 0 || !analyze.OwnedAggregateDirectCountValid(d, len(raw.Args), len(raw.Order)) || len(raw.Args)+len(raw.Order) > 99 {
			return nil, fmt.Errorf("stuntgres: mismatched frozen aggregate call shape")
		}
		arguments = make([]parser.Expr, 0, len(raw.Args)+len(raw.Order))
		arguments = append(arguments, raw.Args...)
		for _, key := range raw.Order {
			arguments = append(arguments, key.Expr)
		}
	} else if raw.Star != (len(raw.Args) == 0) || raw.Window != nil && (raw.Distinct || len(raw.Order) != 0 || raw.Filter != nil) {
		return nil, fmt.Errorf("stuntgres: mismatched frozen aggregate call shape")
	}
	if _, valid := analyze.SQLCallableShape(read, nil, d, parser.FunctionExpr{Args: arguments}); !valid {
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
	if current.RoutineDefinition() == nil || captured.RoutineDefinition() == nil || current.Strict != captured.Strict || current.Display != captured.Display || current.RoutineDefinition().Kind != captured.RoutineDefinition().Kind || current.RoutineDefinition().Invocation != captured.RoutineDefinition().Invocation || current.PLHandler != captured.PLHandler || current.RoutineDefinition().Standard != captured.RoutineDefinition().Standard || current.RoutineDefinition().Source != captured.RoutineDefinition().Source || !slices.Equal(current.RoutineDefinition().Names, captured.RoutineDefinition().Names) || len(current.RoutineDefinition().Inputs) != len(captured.RoutineDefinition().Inputs) {
		return false, nil
	}
	if current.RoutineDefinition().StandardReturn != captured.RoutineDefinition().StandardReturn {
		return false, nil
	}
	if current.Volatility != captured.Volatility || current.SecurityDefiner != captured.SecurityDefiner || current.Parallel != captured.Parallel || current.Cost != captured.Cost || current.Rows != captured.Rows || !catalog.SameRoutineConfiguration(current.Configuration, captured.Configuration) {
		return false, nil
	}
	if current.RoutineDefinition().Procedure != captured.RoutineDefinition().Procedure || current.RoutineDefinition().ReturnsSet != captured.RoutineDefinition().ReturnsSet || current.RoutineDefinition().TableResult != captured.RoutineDefinition().TableResult {
		return false, nil
	}
	if captured.RoutineDefinition().Standard && !slices.Equal(current.RoutineDefinition().Statements, captured.RoutineDefinition().Statements) {
		return false, nil
	}
	result := typ(captured.RoutineDefinition().Result.Type)
	if result == nil || result.ID() != current.RoutineDefinition().Result.Type.ID() {
		return false, nil
	}
	for i, input := range captured.RoutineDefinition().Inputs {
		mapped := typ(input.Type)
		if mapped == nil || mapped.ID() != current.RoutineDefinition().Inputs[i].Type.ID() {
			return false, nil
		}
	}
	if len(current.RoutineDefinition().Formals) != len(captured.RoutineDefinition().Formals) {
		return false, nil
	}
	for i, formal := range captured.RoutineDefinition().Formals {
		next := current.RoutineDefinition().Formals[i]
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
	if (current.RoutineDefinition().PL == nil) != (captured.RoutineDefinition().PL == nil) {
		return false, nil
	}
	if captured.RoutineDefinition().PL != nil {
		if !captured.RoutineDefinition().PL.SameLayout(current.RoutineDefinition().PL) {
			return false, nil
		}
		if len(current.RoutineDefinition().PL.Variables) != len(captured.RoutineDefinition().PL.Variables) {
			return false, nil
		}
		for i, variable := range captured.RoutineDefinition().PL.Variables {
			next := current.RoutineDefinition().PL.Variables[i]
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
