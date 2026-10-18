package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type derivedIndexKey struct {
	Definition, Execution expr.Expr
	Dependencies          []ids.ObjectRef
	Attributes            []int32 // Immutable parent layout used by both bound row frames.
}

func (e *Engine) indexExpressionBinder(op *Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *relationDescriptor) *expressionBinder {
	ref := e.catalog.Domain.Ref(auth.catalog.Epoch(), parent.Address())
	columns := make([]queryColumn, len(parent.Columns))
	for i, c := range parent.Columns {
		columns[i] = queryColumn{descriptor: c, slot: i, leaf: 0, physical: i}
	}
	namespace := queryNamespace{columns: columns, qualifiers: []queryQualifier{{name: parent.Name(), original: parent.Name(), schema: auth.catalog.Get(parent.Namespace()).Name(), ref: ref, columns: columns}}, frame: parent.Columns}
	binder := newExpressionBinder(op.Context(), e, input, op, nil)
	binder.indexConsumer, binder.noParameters, binder.queryAuth = true, true, &auth
	binder.queryNamespace = &namespace
	binder.querySources = []queryLeaf{{ref: ref, relation: parent}}
	return binder
}

// Caller holds engine.mu and supplies the same final catalog/parent authority
// used by the enclosing CREATE or TYPE owner.
// Ordering admission belongs to the caller's final selected class/collation,
// not an expression-only default comparator.
func (e *Engine) bindIndexExpression(op *Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *relationDescriptor, raw parser.Expr, literals []literalExpression) (*derivedIndexKey, string, error) {
	binder := e.indexExpressionBinder(op, input, auth, parent)
	binder.checkLiterals = literals
	definition, err := binder.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		return nil, "", err
	}
	if err := rejectVirtualIndexUses(parent, binder.querySources[0].uses); err != nil {
		return nil, "", err
	}
	evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: binder.input, catalog: auth.catalog}
	evaluation.routineAuthority, evaluation.routineChecks = &auth, binder.dependencies
	evaluation.routineRecipe = true
	execution, _, err := preflightCoercionsWithPolicy(&evaluation, definition, true)
	if err != nil {
		return nil, "", err
	}
	// Preflight the complete analyzed definition before narrowing runtime
	// demand, then admit volatility from the surviving physical providers.
	execution, _, err = simplifyConditional(&evaluation, execution, true)
	if err != nil {
		return nil, "", err
	}
	if plannedVolatility(execution) != expr.Immutable {
		return nil, "", sqlError("42P17", "functions in index expression must be marked IMMUTABLE")
	}
	key := &derivedIndexKey{Definition: definition, Execution: execution, Attributes: parent.Attributes}
	if _, err = (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: parent, dependencies: &key.Dependencies}).sql(op.Context(), auth.catalog, definition); err != nil {
		return nil, "", err
	}
	label := "expr"
	switch r := raw.(type) {
	case parser.ColumnExpr:
		label = r.Name.Parts[len(r.Name.Parts)-1].Text
	case parser.SQLJSONExpr:
		label = r.Kind
	case parser.FunctionExpr:
		label = r.Name.Parts[len(r.Name.Parts)-1].Text
	case parser.CallExpr:
		label = r.Name.Parts[len(r.Name.Parts)-1].Text
	}
	return key, label, nil
}

func indexKeyUsage(parent *relationDescriptor, key indexKey) (sqltype.Usage, error) {
	usage, err := indexKeyBaseUsage(parent, key)
	if err != nil {
		return sqltype.Usage{}, err
	}
	return indexKeyCollation(usage, key), nil
}

func indexKeyBaseUsage(parent *relationDescriptor, key indexKey) (sqltype.Usage, error) {
	if key.Derived != nil {
		return key.Derived.Execution.ResultUsage(), nil
	}
	for i, a := range parent.Attributes {
		if a == key.Attribute {
			return parent.Columns[i].Usage, nil
		}
	}
	return sqltype.Usage{}, fmt.Errorf("stuntgres: index attribute is missing")
}

func indexExpressionDepends(d *relationDescriptor, target ids.ObjectAddress) bool {
	if d.SQLIndex == nil {
		return false
	}
	for _, key := range d.SQLIndex.Keys {
		if key.Derived == nil {
			continue
		}
		for _, ref := range key.Derived.Dependencies {
			a := ref.Address
			if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}

func (d *indexDefinition) HasRuntimeDomainConversion() bool {
	if d.Predicate != nil && d.Predicate.Execution.Properties().RuntimeDomainConversion {
		return true
	}
	return d.HasRuntimeDomainKeyConversion()
}

// Callable index recipes must run on the produced tuple before RETURNING.
// Reuse the tracked insertion output so publication does not call them twice.
// Plain-column indexes retain their allocation-free insertion path.
func (d *indexDefinition) RequiresInsertionOutput() bool {
	if d.HasRuntimeDomainConversion() {
		return true
	}
	if d.Predicate != nil {
		for _, ref := range d.Predicate.Dependencies {
			if ref.Address.ClassID == ids.ProcedureClassID {
				return true
			}
		}
	}
	for _, key := range d.Keys {
		if key.Derived == nil {
			continue
		}
		for _, ref := range key.Derived.Dependencies {
			if ref.Address.ClassID == ids.ProcedureClassID {
				return true
			}
		}
	}
	return false
}

func (d *indexDefinition) HasRuntimeDomainKeyConversion() bool {
	for _, key := range d.Keys {
		if key.Derived != nil && key.Derived.Execution.Properties().RuntimeDomainConversion {
			return true
		}
	}
	return false
}
