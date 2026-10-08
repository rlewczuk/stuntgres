package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type columnTransformation struct {
	attribute  int32
	expression BoundExpr
	rewrite    bool
}

// Binding retains the statement-original row namespace, even after DROP passes.
// No Eval/provider callback runs under Engine.mu.
func (e *Engine) alterTypeCandidate(scope *StatementScope, op *Operation, auth AuthorizationView, stage *CatalogStage, original, next *relationDescriptor, ref ObjectRef, a parser.AlterTableAction) (*relationDescriptor, columnTransformation, error) {
	fail := func(err error) (*relationDescriptor, columnTransformation, error) {
		return nil, columnTransformation{}, err
	}
	slot, ok := next.index[a.Column.Text]
	if !ok {
		return fail(sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Column.Text, next.name)))
	}
	source := original.index[a.Column.Text]
	oldUsage := original.columns[source].Usage
	current := next.columns[slot].Usage
	if current.Type.ID() != oldUsage.Type.ID() || current.Typmod != oldUsage.Typmod {
		return fail(sqlError("0A000", fmt.Sprintf(`cannot alter type of column "%s" twice`, a.Column.Text)))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Epoch != e.state.epoch {
		return fail(ErrCatalogReplaced)
	}
	target := ref.Address
	target.SubID = original.attributes[source]
	if edges := viewDependencyClosure(auth.catalog, []ObjectAddress{target}, stage); len(edges) != 0 {
		view := edges[0].view
		name := view.name
		if parser.IdentifierNeedsQuotes(name) {
			name = quoteIdentifier(name)
		}
		namespace := auth.catalog.state.descriptors[view.namespace].Name()
		path, err := e.lookupPath(auth, relationLookup)
		if err != nil {
			return fail(err)
		}
		visible := false
		for _, schema := range path {
			found := auth.catalog.Lookup(NameKey{ClassID: RelationClassID, Namespace: schema, Name: view.name})
			if len(found) != 0 {
				visible = len(found) == 1 && found[0] == view.address
				break
			}
		}
		if !visible {
			if parser.IdentifierNeedsQuotes(namespace) {
				namespace = quoteIdentifier(namespace)
			}
			name = namespace + "." + name
		}
		d := sqlError("0A000", "cannot alter type of a column used by a view or rule").(*sqlstate.Error)
		d.Detail = fmt.Sprintf(`rule _RETURN on view %s depends on column "%s"`, name, a.Column.Text)
		return fail(d)
	}
	usage, err := e.resolveType(a.TypeName, op)
	if err != nil {
		return fail(err)
	}
	usage, err = e.resolveColumnCollation(usage, a.Collation, op)
	if err != nil {
		return fail(err)
	}
	b := newExpressionBinder(op.Context(), e, scope.input, op, nil)
	b.relation, b.ref, b.noParameters, b.queryAuth = original, ref, true, &auth
	var node BoundExpr
	if a.Using == nil {
		node = columnExpression{expressionInfo{usage: oldUsage, properties: ExprProperties{RowDependent: true}}, ref, 1, 1, source}
	} else {
		columns := make([]queryColumn, len(original.columns))
		for i, c := range original.columns {
			columns[i] = queryColumn{c, i, 0, i}
		}
		qualifier := queryQualifier{name: original.name, original: original.name, schema: auth.catalog.state.descriptors[original.namespace].Name(), ref: ref, columns: columns}
		namespace := queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: original.columns}
		b.queryNamespace = &namespace
		b.querySources = []queryLeaf{{ref: ref, relation: original}}
		node, err = b.bind(a.Using, expressionPolicy{context: projectionContext, expected: usage.Type.Usage()})
		if err != nil {
			return fail(err)
		}
	}
	plan, err := sqltype.PlanCoercion(node.ResultUsage(), usage, sqltype.Assignment)
	if err != nil {
		if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" {
			d = sqlError("42804", fmt.Sprintf(`column "%s" cannot be cast automatically to type %s`, a.Column.Text, usage.Type.DiagnosticName())).(*sqlstate.Error)
			declaration, _ := usage.Declaration()
			if usage.Type.ID() == sqltype.VarChar || usage.Type.ID() == sqltype.BPChar {
				declaration = usage.Type.DiagnosticName() + strings.TrimPrefix(declaration, usage.Type.SQLName())
			}
			identifier := a.Column.Text
			if parser.IdentifierNeedsQuotes(identifier) {
				identifier = quoteIdentifier(identifier)
			}
			d.Hint = fmt.Sprintf(`You might need to specify "USING %s::%s".`, identifier, strings.ToLower(declaration))
			if a.Using != nil {
				d.Message = fmt.Sprintf(`result of USING clause for column "%s" cannot be cast automatically to type %s`, a.Column.Text, usage.Type.DiagnosticName())
				d.Hint = "You might need to add an explicit cast."
			}
			return fail(d)
		}
		return fail(err)
	}
	node = coercionExpression{b.info(usage, parser.Position{}, coercionProperties(node, plan)), node, plan, sqltype.Assignment}
	attribute := original.attributes[source]
	transformation := columnTransformation{attribute, node, a.Using != nil || oldUsage.Type != usage.Type || oldUsage.Typmod != usage.Typmod}
	if usage == current {
		return next, transformation, nil
	}
	replacement := *next
	replacement.columns = append([]ColumnDescriptor(nil), next.columns...)
	replacement.columns[slot].Usage = usage
	if definition, exists := next.defaults[attribute]; exists {
		expression := definition.expression
		for {
			wrapper, ok := expression.(coercionExpression)
			if !ok || wrapper.mode == sqltype.Explicit {
				break
			}
			expression = wrapper.child
		}
		defaultPlan, err := sqltype.PlanCoercion(expression.ResultUsage(), usage, sqltype.Assignment)
		if err != nil {
			return fail(sqlError("42804", fmt.Sprintf(`default for column "%s" cannot be cast automatically to type %s`, a.Column.Text, usage.Type.DiagnosticName())))
		}
		expression = coercionExpression{b.info(usage, parser.Position{}, coercionProperties(expression, defaultPlan)), expression, defaultPlan, sqltype.Assignment}
		canonical, err := defaultSQL(op.Context(), auth.catalog, expression)
		if err != nil {
			return fail(err)
		}
		replacement.defaults = make(map[int32]columnDefault, len(next.defaults))
		for id, d := range next.defaults {
			replacement.defaults[id] = d
		}
		replacement.defaults[attribute] = columnDefault{expression, canonical}
	}
	return &replacement, transformation, nil
}

// Rewrite only effective live heads. Own updates become inserts into the private
// replacement because their committed predecessors are intentionally omitted.
func rewriteTypeRows(scope *StatementScope, op *Operation, old, next *relationDescriptor, store *relationStorage, frontier CommitSeq, transforms []columnTransformation) (*relationStorage, []rowVersion, *privateDeletionRoot, error) {
	if store.generation == ^uint64(0) {
		return nil, nil, nil, fmt.Errorf("stuntgres: storage generation exhausted")
	}
	slots := make([]int, len(next.attributes))
	destinations := make([]int, len(transforms))
	for i, attribute := range next.attributes {
		for j, id := range old.attributes {
			if id == attribute {
				slots[i] = j
				break
			}
		}
	}
	for i, transform := range transforms {
		for slot, attribute := range next.attributes {
			if transform.attribute == attribute {
				destinations[i] = slot
				slots[slot] = -1
				break
			}
		}
	}
	writes, deletes := scope.ownWrites[old.address], scope.ownDeletes[old.address]
	replaced := make(map[RowID]bool)
	for _, row := range writes {
		if row.base != 0 {
			replaced[row.row] = true
		}
	}
	evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output}
	convert := func(input []rowVersion, committed bool) ([]rowVersion, error) {
		result := make([]rowVersion, 0, len(input))
		for _, row := range input {
			if err := op.Context().Err(); err != nil {
				return nil, err
			}
			if committed && replaced[row.row] || !liveVersion(row, frontier, store.ends, deletes) {
				continue
			}
			evaluation.Row = row.values
			values := make([]sqltype.Value, len(slots))
			for i, slot := range slots {
				if slot >= 0 {
					values[i] = row.values[slot]
				}
			}
			for i, transform := range transforms {
				v, err := transform.expression.Eval(&evaluation)
				if err != nil {
					return nil, err
				}
				values[destinations[i]] = v
			}
			row.values = values
			if !committed {
				row.base = 0
			}
			result = append(result, row)
		}
		return result, nil
	}
	committed, err := convert(store.versions, true)
	if err != nil {
		return nil, nil, nil, err
	}
	own, err := convert(writes, false)
	if err != nil {
		return nil, nil, nil, err
	}
	return &relationStorage{versions: committed, generation: store.generation + 1}, own, nil, nil
}
