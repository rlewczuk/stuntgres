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

type alterTypePreparation struct {
	usage      sqltype.Usage
	expression BoundExpr
}

// Identity AS before-statements precede the table passes, but raw USING binding
// precedes AS. Capture once against the immutable original row; never Eval here.
func (e *Engine) prepareAlterIdentityTypes(scope *StatementScope, op *Operation, auth AuthorizationView, original *relationDescriptor, ref ObjectRef, actions []parser.AlterTableAction) ([]alterTypePreparation, *CatalogStage, error) {
	count, hasIdentity := 0, false
	for _, action := range actions {
		if action.Action != parser.AlterColumnType {
			continue
		}
		count++
		if slot, exists := original.index[action.Column.Text]; exists {
			_, identity := original.identities[original.attributes[slot]]
			hasIdentity = hasIdentity || identity
		}
	}
	if !hasIdentity {
		return nil, nil, nil
	}
	prepared := make([]alterTypePreparation, 0, count)
	var namespace *queryNamespace
	e.mu.Lock()
	if ref.Epoch != e.state.epoch {
		e.mu.Unlock()
		return nil, nil, ErrCatalogReplaced
	}
	for _, action := range actions {
		if action.Action != parser.AlterColumnType {
			continue
		}
		if _, exists := original.index[action.Column.Text]; !exists {
			e.mu.Unlock()
			return nil, nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, action.Column.Text, original.name))
		}
		usage, err := e.resolveType(action.TypeName, op)
		if err != nil {
			e.mu.Unlock()
			return nil, nil, err
		}
		if action.Using != nil && namespace == nil {
			namespace = alterTypeNamespace(auth, original, ref)
		}
		node, err := e.bindAlterTypeUsingLocked(scope, auth, original, ref, action, usage, namespace)
		if err != nil {
			e.mu.Unlock()
			return nil, nil, err
		}
		prepared = append(prepared, alterTypePreparation{usage, node})
	}
	e.mu.Unlock()
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	index := 0
	for _, action := range actions {
		if action.Action != parser.AlterColumnType {
			continue
		}
		slot := original.index[action.Column.Text]
		attribute := original.attributes[slot]
		if identity, exists := original.identities[attribute]; exists {
			if err := e.alterIdentityTypeSequence(scope, stage, original, attribute, identity.sequence, prepared[index].usage); err != nil {
				return nil, stage, err
			}
		}
		index++
	}
	return prepared, stage, nil
}

func alterTypeNamespace(auth AuthorizationView, original *relationDescriptor, ref ObjectRef) *queryNamespace {
	columns := make([]queryColumn, len(original.columns))
	for i, column := range original.columns {
		columns[i] = queryColumn{descriptor: column, slot: i, leaf: queryTargetLeaf, physical: i}
	}
	qualifier := queryQualifier{name: original.name, original: original.name, schema: auth.catalog.state.descriptors[original.namespace].Name(), ref: ref, columns: columns}
	return &queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: original.columns}
}

func (e *Engine) bindAlterTypeUsingLocked(scope *StatementScope, auth AuthorizationView, original *relationDescriptor, ref ObjectRef, action parser.AlterTableAction, usage sqltype.Usage, namespace *queryNamespace) (BoundExpr, error) {
	if action.Using == nil {
		source := original.index[action.Column.Text]
		return columnExpression{expressionInfo: expressionInfo{usage: original.columns[source].Usage, properties: ExprProperties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: source}.withGeneration(original, source, 0), nil
	}
	if namespace == nil {
		namespace = alterTypeNamespace(auth, original, ref)
	}
	binder := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
	binder.relation, binder.ref, binder.noParameters, binder.queryAuth = original, ref, true, &auth
	binder.queryNamespace = namespace
	binder.querySources = []queryLeaf{{ref: ref, relation: original}}
	node, err := binder.bind(action.Using, expressionPolicy{context: projectionContext, expected: usage.Type.Usage()})
	if err == nil && binder.regclassConcat {
		err = e.captureCommentSnapshotLocked(scope)
	}
	return node, err
}

// Binding retains the statement-original row namespace, even after DROP passes.
// No Eval/provider callback runs under Engine.mu.
func (e *Engine) alterTypeCandidate(scope *StatementScope, op *Operation, auth AuthorizationView, stage *CatalogStage, original, next *relationDescriptor, ref ObjectRef, a parser.AlterTableAction, prepared *alterTypePreparation) (*relationDescriptor, columnTransformation, error) {
	fail := func(err error) (*relationDescriptor, columnTransformation, error) {
		return nil, columnTransformation{}, err
	}
	slot, ok := next.index[a.Column.Text]
	if !ok {
		return fail(sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Column.Text, next.name)))
	}
	if _, generated := next.generations[next.attributes[slot]]; generated {
		if a.Using != nil {
			diagnostic := expressionError("42611", "cannot specify USING when altering type of generated column", a.Column.Pos).(*sqlstate.Error)
			diagnostic.Detail = fmt.Sprintf(`Column "%s" is a generated column.`, a.Column.Text)
			return fail(diagnostic)
		}
		return fail(sqlError("0A000", "altering type of a generated column is unsupported"))
	}
	for _, attribute := range next.attributes {
		definition, generated := next.generations[attribute]
		if !generated {
			continue
		}
		for _, dependency := range definition.dependencies {
			if dependency.Address.ClassID == next.address.ClassID && dependency.Address.ObjectID == next.address.ObjectID && dependency.Address.SubID == next.attributes[slot] {
				diagnostic := sqlError("0A000", "cannot alter type of a column used by a generated column").(*sqlstate.Error)
				diagnostic.Detail = fmt.Sprintf(`Column "%s" is used by generated column "%s".`, a.Column.Text, next.columns[next.indexForAttribute(attribute)].Name)
				return fail(diagnostic)
			}
		}
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
	if err := policyTypeChangeError(auth.catalog, target); err != nil {
		return fail(err)
	}
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
	var usage sqltype.Usage
	var err error
	if prepared != nil {
		usage = prepared.usage
	} else {
		usage, err = e.resolveType(a.TypeName, op)
		if err != nil {
			return fail(err)
		}
	}
	usage, err = e.resolveColumnCollation(usage, a.Collation, op)
	if err != nil {
		return fail(err)
	}
	var node BoundExpr
	if prepared != nil {
		node = prepared.expression
	} else {
		node, err = e.bindAlterTypeUsingLocked(scope, auth, original, ref, a, usage, nil)
		if err != nil {
			return fail(err)
		}
	}
	input, annotations := domainCoercionInput(node, usage)
	info, err := storageCoercionInfo(auth.catalog, expressionInfo{usage: usage, properties: input.Properties()}, input)
	if err != nil {
		return fail(err)
	}
	plan, err := sqltype.PlanCoercion(input.ResultUsage(), info.usage, sqltype.Assignment)
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
	if plan.RequiresCatalogOutput() {
		if err := e.captureCommentSnapshotLocked(scope); err != nil {
			return fail(err)
		}
	}
	info.properties = coercionProperties(input, plan)
	node = restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, sqltype.Assignment})
	node, err = storageDestination(node, usage, info.pos)
	if err != nil {
		return fail(err)
	}
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
		input, annotations := domainCoercionInput(expression, usage)
		info, err := storageCoercionInfo(auth.catalog, expressionInfo{usage: usage, properties: input.Properties()}, input)
		if err != nil {
			return fail(err)
		}
		defaultPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.usage, sqltype.Assignment)
		if err != nil {
			return fail(sqlError("42804", fmt.Sprintf(`default for column "%s" cannot be cast automatically to type %s`, a.Column.Text, usage.Type.DiagnosticName())))
		}
		info.properties = coercionProperties(input, defaultPlan)
		expression = restoreDomainCastCollations(annotations, coercionExpression{info, input, defaultPlan, sqltype.Assignment})
		expression, err = storageDestination(expression, usage, info.pos)
		if err != nil {
			return fail(err)
		}
		definition.dependencies = nil
		definition.catalogSnapshot = false
		_, err = (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, dependencies: &definition.dependencies, commentSnapshot: &definition.catalogSnapshot}).sql(op.Context(), auth.catalog, expression)
		if err != nil {
			return fail(err)
		}
		replacement.defaults = make(map[int32]columnDefault, len(next.defaults))
		for id, d := range next.defaults {
			replacement.defaults[id] = d
		}
		definition.expression = expression
		definition.OID = 0
		replacement.defaults[attribute] = definition
	}
	if err := e.captureCheckCommentSnapshotLocked(scope, &replacement); err != nil {
		return fail(err)
	}
	return &replacement, transformation, nil
}

// Rewrite only effective live heads. Own updates become inserts into the private
// replacement because their committed predecessors are intentionally omitted.
func rewriteTypeRows(scope *StatementScope, op *Operation, old, next *relationDescriptor, store *relationStorage, frontier CommitSeq, transforms []columnTransformation, additions []addedColumn, generations []int32, validator alterRowValidator) (*relationStorage, []rowVersion, *privateDeletionRoot, error) {
	if store.generation == ^uint64(0) {
		return nil, nil, nil, fmt.Errorf("stuntgres: storage generation exhausted")
	}
	slots := make([]int, len(next.attributes))
	destinations := make([]int, len(transforms))
	addedSlots := make([]int, len(additions))
	generatedSlots := make([]int, len(generations))
	for i, attribute := range generations {
		generatedSlots[i] = next.indexForAttribute(attribute)
	}
	for i, added := range additions {
		addedSlots[i] = next.index[added.column.Name]
	}
	for i, attribute := range next.attributes {
		slots[i] = -1
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
	writes, deletes := scope.columnProjectionRoots(old.address)
	var replaced map[RowID]bool
	for _, row := range writes {
		if row.base != 0 {
			if replaced == nil {
				replaced = make(map[RowID]bool)
			}
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
			for i, added := range additions {
				value := added.value
				if added.expression != nil && added.expression.Properties().Volatility > Stable {
					var err error
					value, err = added.expression.Eval(&evaluation)
					if err != nil {
						return nil, err
					}
				}
				values[addedSlots[i]] = value
			}
			evaluation.Row = values
			for i, attribute := range generations {
				value, err := next.generations[attribute].planned.Eval(&evaluation)
				if err != nil {
					return nil, err
				}
				values[generatedSlots[i]] = value
			}
			if err := validator.validate(values, &evaluation); err != nil {
				return nil, err
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
