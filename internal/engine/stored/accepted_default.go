package stored

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// AcceptedDefaultRoutine authenticates one owned original DEFAULT occurrence.
// Neither a relation receipt alone nor a same-address procedure grants this
// lifetime. Every changed procedure must occur in this exact source recipe and
// traverse the same completed scalar publications as the relation.
func AcceptedDefaultRoutine(read catalog.Read, artifact *exec.AcceptedDefault, occurrence exec.AcceptedDefaultCall) (*catalog.Procedure, error) {
	if err := AcceptedDefaultSource(read, artifact); err != nil {
		return nil, err
	}
	original, ref, attribute, definition := artifact.Source()
	call, path, ok := occurrence.Original()
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	sourceCall, found := defaultCallAtPath(read, definition.Expression, path)
	if !found || !sameDefaultSourceCall(sourceCall, call) {
		return nil, ids.ErrCatalogReplaced
	}
	current, _ := read.Relation(ref.Address)
	live, err := pairedDefaultRoutine(read, original, current, attribute, definition.OID, sourceCall, path)
	if err != nil {
		return nil, err
	}
	if err := (ProcedureProvider{}).Validate(read, live); err != nil {
		return nil, err
	}
	return live, nil
}

// AcceptedDefaultSource also validates literal/operator-only recipes. Paired
// routine admission is deliberately not sufficient authority for their types,
// collations, casts or independent provider dependencies.
func AcceptedDefaultSource(read catalog.Read, artifact *exec.AcceptedDefault) error {
	if !artifact.Valid() || read.Owner() == nil {
		return ids.ErrCatalogReplaced
	}
	original, ref, attribute, definition := artifact.Source()
	if original == nil || ref.Domain() != read.Owner().Domain || ref.Epoch != read.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := read.Relation(ref.Address)
	if !ok || !ScalarRelationSuccessor(read, original, current) || current.ColumnMeta.Defaults[attribute].OID != definition.OID {
		return ids.ErrCatalogReplaced
	}
	var invalid error
	validate := func(node expr.Expr) {
		if invalid == nil && (!ValidExpressionUsage(read, node.ResultUsage()) || ValidateUsageCollation(read, node.ResultUsage()) != nil) {
			invalid = ids.ErrCatalogReplaced
		}
	}
	plan.WalkExpression(definition.Expression, validate)
	plan.WalkExpression(artifact.Expression(), validate)
	if invalid != nil {
		return invalid
	}
	if len(artifact.Dependencies()) != len(definition.Dependencies) {
		return ids.ErrCatalogReplaced
	}
	for i, dependency := range artifact.Dependencies() {
		originalDependency := artifact.SourceDescriptor(dependency.Ref)
		if dependency.Ref != definition.Dependencies[i] || dependency.Purpose != catalog.DefinitionDependency || !ReferenceEpochValid(read, original, dependency.Ref) || originalDependency == nil || originalDependency.Revision() != dependency.Revision || originalDependency.Address() != dependency.Ref.Address {
			return ids.ErrCatalogReplaced
		}
		d := read.Get(dependency.Ref.Address)
		if d == nil {
			return ids.ErrCatalogReplaced
		}
		if d.Revision() == dependency.Revision {
			continue
		}
		if old, ok := originalDependency.(*catalog.Procedure); ok {
			if next, ok := d.(*catalog.Procedure); ok && exec.SameRoutineDeclaration(next, old) {
				continue
			}
		}
		if old, ok := originalDependency.(*catalog.Type); ok {
			if next, ok := d.(*catalog.Type); ok && (!next.UsesDomainPlanStamp() && next.SQLType == old.SQLType || catalog.SamePreparedTypeDefinition(old, next)) {
				continue
			}
		}
		var source plan.SQLRoutineExpr
		var path []int
		if err := walkDefaultSource(read, definition.Expression, nil, func(n plan.SQLRoutineExpr, originalPath []int) {
			if source.Procedure == nil && n.Ref == dependency.Ref && n.Procedure.Revision() == dependency.Revision {
				source, path = n, slices.Clone(originalPath)
			}
		}); err != nil {
			return err
		}
		if source.Procedure == nil {
			return ids.ErrCatalogReplaced
		}
		if _, err := pairedDefaultRoutine(read, original, current, attribute, definition.OID, source, path); err != nil {
			return err
		}
	}
	return nil
}

func defaultCallAtPath(read catalog.Read, root expr.Expr, path []int) (plan.SQLRoutineExpr, bool) {
	for _, step := range path {
		if step < 0 || root == nil {
			return plan.SQLRoutineExpr{}, false
		}
		if call, ok := root.(plan.SQLRoutineExpr); ok {
			if step >= len(call.Args) {
				return plan.SQLRoutineExpr{}, false
			}
			root = call.Args[step]
			continue
		}
		var next expr.Expr
		index := 0
		_, _, err := MapExpressionChildren(read, root, func(child expr.Expr) (expr.Expr, bool, error) {
			if index == step {
				next = child
			}
			index++
			return child, false, nil
		})
		if err != nil || next == nil {
			return plan.SQLRoutineExpr{}, false
		}
		root = next
	}
	call, ok := root.(plan.SQLRoutineExpr)
	return call, ok
}

func sameDefaultSourceCall(n, original plan.SQLRoutineExpr) bool {
	return exec.SameRoutineDeclaration(n.Procedure, original.Procedure) && n.Ref == original.Ref && n.Source == original.Source && n.Occurrence == original.Occurrence && n.Usage == original.Usage && n.InputCollation == original.InputCollation && n.SameArguments(original)
}

// Dependency enumeration is over the authentic complete source. Invocation
// admission resolves the preparation token's one original path directly.
func walkDefaultSource(read catalog.Read, root expr.Expr, path []int, visit func(plan.SQLRoutineExpr, []int)) error {
	if root == nil {
		return nil
	}
	if call, ok := root.(plan.SQLRoutineExpr); ok {
		visit(call, path)
		for i, child := range call.Args {
			if err := walkDefaultSource(read, child, append(path, i), visit); err != nil {
				return err
			}
		}
		return nil
	}
	index := 0
	_, _, err := MapExpressionChildren(read, root, func(child expr.Expr) (expr.Expr, bool, error) {
		step := index
		index++
		return child, false, walkDefaultSource(read, child, append(path, step), visit)
	})
	return err
}

func pairedDefaultRoutine(read catalog.Read, original, current *catalog.Relation, attribute int32, oid ids.OID, call plan.SQLRoutineExpr, path []int) (*catalog.Procedure, error) {
	live, ok := read.Procedure(call.Ref.Address)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	expected := live
	for relation := current; relation != original; {
		publication, ok := relation.ScalarPublication.(*ScalarRoutinePublication)
		if !ok || publication == nil || publication.before == nil || publication.after == nil || publication.source.Owner() != read.Owner() || publication.source.Epoch() != read.Epoch() || publication.completed.Get(relation.Address()) != relation {
			return nil, ids.ErrCatalogReplaced
		}
		previous, ok := publication.source.Relation(relation.Address())
		if !ok || previous.Revision() >= relation.Revision() || publication.source.Get(publication.before.Address()) != publication.before || publication.completed.Get(publication.after.Address()) != publication.after {
			return nil, ids.ErrCatalogReplaced
		}
		before, after := previous.ColumnMeta.Defaults[attribute], relation.ColumnMeta.Defaults[attribute]
		if before.OID != oid || after.OID != oid || before.Expression == nil || after.Expression == nil || !slices.Equal(before.Dependencies, after.Dependencies) {
			return nil, ids.ErrCatalogReplaced
		}
		if publication.before.Address() == call.Ref.Address {
			if !exec.SameRoutineDeclaration(expected, publication.after) {
				return nil, ids.ErrCatalogReplaced
			}
			mappedCall := call
			mappedCall.Procedure, mappedCall.RoutineReference = publication.before, publication.before
			if source, ok := defaultCallAtPath(read, before.Expression, path); !ok || !sameDefaultSourceCall(source, mappedCall) {
				return nil, ids.ErrCatalogReplaced
			}
			mappedCall.Procedure, mappedCall.RoutineReference = publication.after, publication.after
			if target, ok := defaultCallAtPath(read, after.Expression, path); !ok || !sameDefaultSourceCall(target, mappedCall) {
				return nil, ids.ErrCatalogReplaced
			}
			expected = publication.before
		}
		relation = previous
	}
	if !exec.SameRoutineDeclaration(expected, call.Procedure) {
		return nil, ids.ErrCatalogReplaced
	}
	// Only body-bearing declaration fields may differ through the authenticated
	// replacement. ABI, strictness, volatility, language and provider remain exact.
	compatible := *live
	compatible.SQL, compatible.SQLBody, compatible.SQLDefaults = call.Procedure.SQL, call.Procedure.SQLBody, call.Procedure.SQLDefaults
	compatible.DeclarationBody, compatible.DeclarationDependencies, compatible.Body = call.Procedure.DeclarationBody, call.Procedure.DeclarationDependencies, call.Procedure.Body
	compatible.Display.Source = call.Procedure.Display.Source
	if !exec.SameRoutineDeclaration(&compatible, call.Procedure) {
		return nil, ids.ErrCatalogReplaced
	}
	return live, nil
}
