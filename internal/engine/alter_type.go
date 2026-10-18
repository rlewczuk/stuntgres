package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type columnTransformation struct {
	attribute    int32
	expression   BoundExpr
	dependencies []expressionDependency // Original USING binder manifest; borrowed by serial execution preparation.
	rewrite      bool
}

type alterTypePreparation struct {
	usage              sqltype.Usage
	transformation     columnTransformation
	declaredDimensions int16
}

// Phase-one lookup uses the statement-original descriptor, before ADD/DROP passes.
func alterTypeColumn(original *relationDescriptor, name string) (int, error) {
	slot, ok := original.index[name]
	if !ok {
		return 0, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, original.name))
	}
	return slot, nil
}

// Sequence AS uses admitted target usages, including the prefix retained when
// transformation completion fails. It never visits an unadmitted later TYPE.
func (e *Engine) stageAlterIdentityTypes(scope *StatementScope, op *Operation, auth AuthorizationView, original *relationDescriptor, actions []parser.AlterTableAction, prepared []alterTypePreparation) (*CatalogStage, error) {
	var stage *CatalogStage
	index := 0
	for _, action := range actions {
		if action.Action != parser.AlterColumnType {
			continue
		}
		if index == len(prepared) {
			break
		}
		slot := original.index[action.Column.Text]
		attribute := original.attributes[slot]
		if identity, exists := original.identities[attribute]; exists {
			if stage == nil {
				if scope.inheritanceAlter != nil {
					stage = scope.inheritanceAlter.stage
				} else {
					stage = e.BeginCatalogStage(auth.catalog)
					stage.authorizeLocked(auth, op.session)
				}
			}
			if err := e.alterIdentityTypeSequence(scope, stage, original, attribute, identity.sequence, prepared[index].usage); err != nil {
				return stage, err
			}
		}
		index++
	}
	return stage, nil
}

func alterTypeNamespace(auth AuthorizationView, original *relationDescriptor, ref ids.ObjectRef) *queryNamespace {
	columns := make([]queryColumn, len(original.columns))
	for i, column := range original.columns {
		columns[i] = queryColumn{descriptor: column, slot: i, leaf: queryTargetLeaf, physical: i}
	}
	qualifier := queryQualifier{name: original.name, original: original.name, schema: auth.catalog.Get(original.namespace).Name(), ref: ref, columns: columns}
	return &queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: original.columns}
}

func (e *Engine) bindAlterTypeUsingLocked(scope *StatementScope, auth AuthorizationView, original *relationDescriptor, ref ids.ObjectRef, action parser.AlterTableAction, usage sqltype.Usage, namespace *queryNamespace) (BoundExpr, []expressionDependency, error) {
	if action.Using == nil {
		source := original.index[action.Column.Text]
		return columnExpression{Info: expressionInfo{Usage: original.columns[source].Usage, Property: ExprProperties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: source}.withGeneration(original, source, 0), nil, nil
	}
	if namespace == nil {
		namespace = alterTypeNamespace(auth, original, ref)
	}
	binder := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
	binder.relation, binder.ref, binder.noParameters, binder.queryAuth = original, ref, true, &auth
	binder.queryNamespace = namespace
	binder.querySources = []queryLeaf{{ref: ref, relation: original}}
	node, err := binder.bind(action.Using, expressionPolicy{context: projectionContext, expected: usage.Type.Usage()})
	if err == nil && (binder.regclassConcat || binder.catalogSnapshot) {
		err = e.captureCommentSnapshotLocked(scope)
	}
	return node, binder.dependencies, err
}

// Complete phase-one TYPE work against the immutable original row. The caller
// holds Engine.mu; neither row evaluation nor candidate mutation belongs here.
// suppliedUsage is used only by typed propagation of an already resolved type.
func (e *Engine) prepareAlterTypeLocked(scope *StatementScope, op *Operation, auth AuthorizationView, original *relationDescriptor, ref ids.ObjectRef, a parser.AlterTableAction, suppliedUsage *sqltype.Usage) (alterTypePreparation, error) {
	prepared, err := e.admitAlterTypeLocked(scope, op, auth, original, ref, a, suppliedUsage)
	if err != nil {
		return alterTypePreparation{}, err
	}
	return e.completeAlterTypeLocked(scope, auth, original, a, prepared)
}

// Admission resolves the target and binds USING once, before sequence AS.
// Its raw expression is replaced by the completed storage transformation.
func (e *Engine) admitAlterTypeLocked(scope *StatementScope, op *Operation, auth AuthorizationView, original *relationDescriptor, ref ids.ObjectRef, a parser.AlterTableAction, suppliedUsage *sqltype.Usage) (alterTypePreparation, error) {
	fail := func(err error) (alterTypePreparation, error) {
		return alterTypePreparation{}, err
	}
	source, err := alterTypeColumn(original, a.Column.Text)
	if err != nil {
		return fail(err)
	}
	if ref.Epoch != e.catalog.state.epoch {
		return fail(ErrCatalogReplaced)
	}
	if _, generated := original.generations[original.attributes[source]]; generated {
		if a.Using != nil {
			diagnostic := expressionError("42611", "cannot specify USING when altering type of generated column", a.Column.Pos).(*sqlstate.Error)
			diagnostic.Detail = fmt.Sprintf(`Column "%s" is a generated column.`, a.Column.Text)
			return fail(diagnostic)
		}
		return fail(sqlError("0A000", "altering type of a generated column is unsupported"))
	}
	var usage sqltype.Usage
	if suppliedUsage != nil {
		usage = *suppliedUsage
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
	prepared := alterTypePreparation{usage: usage, declaredDimensions: int16(len(a.TypeName.ArrayBounds))}
	if original.relationKind == compositeRelation {
		return prepared, nil
	}
	if original.relationKind == foreignRelation {
		if a.Using != nil {
			if _, _, err := e.bindAlterTypeUsingLocked(scope, auth, original, ref, a, usage, nil); err != nil {
				return fail(err)
			}
			return fail(sqlError("42809", fmt.Sprintf(`"%s" is not a table`, original.name)))
		}
		return prepared, nil
	}
	node, dependencies, err := e.bindAlterTypeUsingLocked(scope, auth, original, ref, a, usage, nil)
	if err != nil {
		return fail(err)
	}
	prepared.transformation = columnTransformation{attribute: original.attributes[source], expression: node, dependencies: dependencies}
	return prepared, nil
}

// Engine.mu remains held. No expression is evaluated during completion.
func (e *Engine) completeAlterTypeLocked(scope *StatementScope, auth AuthorizationView, original *relationDescriptor, a parser.AlterTableAction, prepared alterTypePreparation) (alterTypePreparation, error) {
	fail := func(err error) (alterTypePreparation, error) {
		return alterTypePreparation{}, err
	}
	if original.relationKind == compositeRelation || original.relationKind == foreignRelation {
		return prepared, nil
	}
	usage := prepared.usage
	node := prepared.transformation.expression
	input, annotations := domainCoercionInput(node, usage)
	info, err := storageCoercionInfo(auth.catalog, expressionInfo{Usage: usage, Property: input.Properties()}, input)
	if err != nil {
		return fail(err)
	}
	plan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
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
	info.Property = coercionProperties(input, plan)
	node = restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, sqltype.Assignment, false})
	node, err = storageDestination(node, usage, info.Pos)
	if err != nil {
		return fail(err)
	}
	source := original.index[a.Column.Text]
	oldUsage := original.columns[source].Usage
	prepared.transformation.expression = node
	prepared.transformation.rewrite = a.Using != nil || oldUsage.Type != usage.Type || oldUsage.Typmod != usage.Typmod
	return prepared, nil
}

// Candidate-only mutation consumes the completed phase-one result.
func (e *Engine) alterTypeCandidate(scope *StatementScope, op *Operation, auth AuthorizationView, stage *CatalogStage, original, next *relationDescriptor, ref ids.ObjectRef, a parser.AlterTableAction, prepared alterTypePreparation) (*relationDescriptor, columnTransformation, error) {
	fail := func(err error) (*relationDescriptor, columnTransformation, error) {
		return nil, columnTransformation{}, err
	}
	slot, err := alterTypeColumn(next, a.Column.Text)
	if err != nil {
		return fail(err)
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
	if ref.Epoch != e.catalog.state.epoch {
		return fail(ErrCatalogReplaced)
	}
	target := ref.Address
	target.SubID = original.attributes[source]
	if err := policyTypeChangeError(auth.catalog, target); err != nil {
		return fail(err)
	}
	if path, pathErr := e.lookupPath(auth, relationLookup); pathErr != nil {
		return fail(pathErr)
	} else if dependents := ruleDependentLabels(auth.catalog, []ids.ObjectAddress{target}, path); len(dependents) != 0 {
		d := sqlError("0A000", "cannot alter type of a column used by a view or rule").(*sqlstate.Error)
		d.Detail = fmt.Sprintf(`%s depends on column "%s"`, dependents[0].label, a.Column.Text)
		return fail(d)
	}
	if edges := viewDependencyClosure(auth.catalog, []ids.ObjectAddress{target}, stage); len(edges) != 0 {
		view := edges[0].view
		name := view.name
		if parser.IdentifierNeedsQuotes(name) {
			name = quoteIdentifier(name)
		}
		namespace := auth.catalog.Get(view.namespace).Name()
		path, err := e.lookupPath(auth, relationLookup)
		if err != nil {
			return fail(err)
		}
		visible := false
		for _, schema := range path {
			found := auth.catalog.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: schema, Name: view.name})
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
	usage := prepared.usage
	if original.relationKind == compositeRelation {
		if usage == current && next.columns[slot].DeclaredDimensions == prepared.declaredDimensions {
			return next, columnTransformation{}, nil
		}
		replacement := *next
		replacement.columns = append([]ColumnDescriptor(nil), next.columns...)
		replacement.columns[slot].Usage = usage
		replacement.columns[slot].DeclaredDimensions = prepared.declaredDimensions
		return &replacement, columnTransformation{}, nil
	}
	attribute := original.attributes[source]
	transformation := prepared.transformation
	dimensions := prepared.declaredDimensions
	if usage == current && next.columns[slot].DeclaredDimensions == dimensions {
		return next, transformation, nil
	}
	replacement := *next
	replacement.columns = append([]ColumnDescriptor(nil), next.columns...)
	replacement.columns[slot].Usage = usage
	replacement.columns[slot].DeclaredDimensions = dimensions
	if usage == current {
		return &replacement, transformation, nil
	}
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
		info, err := storageCoercionInfo(auth.catalog, expressionInfo{Usage: usage, Property: input.Properties()}, input)
		if err != nil {
			return fail(err)
		}
		defaultPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if err != nil {
			return fail(sqlError("42804", fmt.Sprintf(`default for column "%s" cannot be cast automatically to type %s`, a.Column.Text, usage.Type.DiagnosticName())))
		}
		info.Property = coercionProperties(input, defaultPlan)
		expression = restoreDomainCastCollations(annotations, coercionExpression{info, input, defaultPlan, sqltype.Assignment, false})
		expression, err = storageDestination(expression, usage, info.Pos)
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
func rewriteTypeRows(scope *StatementScope, op *Operation, old, next *relationDescriptor, store *relationStorage, frontier ids.CommitSeq, transforms []columnTransformation, additions []addedColumn, generations []int32, validator alterRowValidator) (*relationStorage, []rowVersion, *privateDeletionRoot, error) {
	if store.generation == ^uint64(0) {
		return nil, nil, nil, fmt.Errorf("stuntgres: storage generation exhausted")
	}
	slots := make([]int, len(next.attributes))
	destinations := make([]int, len(transforms))
	addedSlots := make([]struct {
		index  int
		perRow bool
	}, len(additions))
	generatedSlots := make([]int, len(generations))
	for i, attribute := range generations {
		generatedSlots[i] = next.indexForAttribute(attribute)
	}
	for i, added := range additions {
		addedSlots[i].index = next.index[added.column.Name]
		if added.expression != nil {
			properties := added.expression.Properties()
			addedSlots[i].perRow = properties.Volatility > Stable || properties.RuntimeDomainConversion
		}
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
	var replaced map[ids.RowID]bool
	for _, row := range writes {
		if row.base != 0 {
			if replaced == nil {
				replaced = make(map[ids.RowID]bool)
			}
			replaced[row.row] = true
		}
	}
	evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output, catalog: scope.routineAuth.catalog}
	preparedTransforms := false
	var generationProgram []BoundExpr
	preparedGeneration := false
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
			if !preparedTransforms {
				var private []columnTransformation
				for i, transform := range transforms {
					recipe := evaluation
					recipe.routineRecipe, recipe.routineGraft = true, false
					recipe.routineChecks = transform.dependencies
					node, changed, err := preflightCoercionsWithPolicy(&recipe, transform.expression, true)
					evaluation.preparedDomainChecks = recipe.preparedDomainChecks
					if err != nil {
						return nil, err
					}
					if err := validateExpressionJoinExecution(&recipe, node, recipe.catalog); err != nil {
						return nil, err
					}
					if changed {
						if private == nil {
							private = append([]columnTransformation(nil), transforms...)
						}
						private[i].expression = node
					}
				}
				if private != nil {
					transforms = private
				}
				preparedTransforms = true
			}
			values := make([]sqltype.Value, len(slots))
			for i, slot := range slots {
				if slot >= 0 {
					values[i] = row.values[slot]
				}
			}
			for i, transform := range transforms {
				// Lazy SQL bodies still need this transformation's original
				// manifest after preparation has returned.
				recipe := evaluation
				recipe.routineRecipe, recipe.routineGraft = true, false
				recipe.routineChecks = transform.dependencies
				v, err := transform.expression.Eval(&recipe)
				evaluation.preparedDomainChecks = recipe.preparedDomainChecks
				if err != nil {
					return nil, err
				}
				values[destinations[i]] = v
			}
			for i, added := range additions {
				value := added.value
				if addedSlots[i].perRow {
					var err error
					value, err = added.expression.Eval(&evaluation)
					if err != nil {
						return nil, err
					}
				}
				values[addedSlots[i].index] = value
			}
			evaluation.Row = values
			if !preparedGeneration && len(generations) != 0 {
				var err error
				generationProgram, err = prepareGenerationProgram(&evaluation, next, generatedSlots, old)
				if err != nil {
					return nil, err
				}
				preparedGeneration = true
			}
			for i, attribute := range generations {
				expression := generationRecipeExpression(next.generations[attribute])
				if generationProgram != nil {
					expression = generationProgram[generatedSlots[i]]
				}
				value, err := expression.Eval(&evaluation)
				if err != nil {
					return nil, err
				}
				values[generatedSlots[i]] = value
			}
			if err := validator.validate(values, &evaluation); err != nil {
				return nil, err
			}
			row.values = values
			row.indexOutputs = nil
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
