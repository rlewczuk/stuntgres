package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type derivedIndexKey struct {
	definition, execution BoundExpr
	dependencies          []ObjectRef
	attributes            []int32 // Immutable parent layout used by both bound row frames.
}

func (e *Engine) indexExpressionBinder(op *Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *relationDescriptor) *expressionBinder {
	ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: parent.address}
	columns := make([]queryColumn, len(parent.columns))
	for i, c := range parent.columns {
		columns[i] = queryColumn{descriptor: c, slot: i, leaf: 0, physical: i}
	}
	namespace := queryNamespace{columns: columns, qualifiers: []queryQualifier{{name: parent.name, original: parent.name, schema: auth.catalog.state.descriptors[parent.namespace].Name(), ref: ref, columns: columns}}, frame: parent.columns}
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
	evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: input}
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
	if plannedVolatility(execution) != Immutable {
		return nil, "", sqlError("42P17", "functions in index expression must be marked IMMUTABLE")
	}
	key := &derivedIndexKey{definition: definition, execution: execution, attributes: parent.attributes}
	if _, err = (scalarRenderer{owner: parent, dependencies: &key.dependencies}).sql(op.Context(), auth.catalog, definition); err != nil {
		return nil, "", err
	}
	label := "expr"
	switch r := raw.(type) {
	case parser.ColumnExpr:
		label = r.Name.Parts[len(r.Name.Parts)-1].Text
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
	if key.derived != nil {
		return key.derived.execution.ResultUsage(), nil
	}
	for i, a := range parent.attributes {
		if a == key.attribute {
			return parent.columns[i].Usage, nil
		}
	}
	return sqltype.Usage{}, fmt.Errorf("stuntgres: index attribute is missing")
}

func indexExpressionDepends(d *relationDescriptor, target ObjectAddress) bool {
	if d.sqlIndex == nil {
		return false
	}
	for _, key := range d.sqlIndex.keys {
		if key.derived == nil {
			continue
		}
		for _, ref := range key.derived.dependencies {
			a := ref.Address
			if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}
