package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type columnTransformation struct {
	attribute    int32
	expression   expr.Expr
	dependencies []catalog.ExpressionDependency // Original USING binder manifest; borrowed by serial execution preparation.
	rewrite      bool
}

type alterTypePreparation struct {
	usage              sqltype.Usage
	transformation     columnTransformation
	declaredDimensions int16
}

// Phase-one lookup uses the statement-original descriptor, before ADD/DROP passes.
func alterTypeColumn(original *catalog.Relation, name string) (int, error) {
	slot, ok := original.Index[name]
	if !ok {
		return 0, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, original.Name()))
	}
	return slot, nil
}

// Sequence AS uses admitted target usages, including the prefix retained when
// transformation completion fails. It never visits an unadmitted later TYPE.
func (e *Engine) stageAlterIdentityTypes(scope *StatementScope, op *Operation, auth AuthorizationView, original *catalog.Relation, actions []parser.AlterTableAction, prepared []alterTypePreparation) (*CatalogStage, error) {
	var stage *CatalogStage
	index := 0
	for _, action := range actions {
		if action.Action != parser.AlterColumnType {
			continue
		}
		if index == len(prepared) {
			break
		}
		slot := original.Index[action.Column.Text]
		attribute := original.Attributes[slot]
		if identity, exists := original.ColumnMeta.Identities[attribute]; exists {
			if stage == nil {
				if scope.inheritance.alter != nil {
					stage = scope.inheritance.alter.stage
				} else {
					stage = e.BeginCatalogStage(auth.Catalog)
					stage.authorizeLocked(auth, op.Session)
				}
			}
			if err := e.alterIdentityTypeSequence(scope, stage, original, attribute, identity.Sequence, prepared[index].usage); err != nil {
				return stage, err
			}
		}
		index++
	}
	return stage, nil
}

func alterTypeNamespace(auth AuthorizationView, original *catalog.Relation, ref ids.ObjectRef) *analyze.QueryNamespace {
	columns := make([]analyze.QueryColumn, len(original.Columns))
	for i, column := range original.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: column, Slot: i, Leaf: analyze.QueryTargetLeaf, Physical: i}
	}
	qualifier := analyze.QueryQualifier{Name: original.Name(), Original: original.Name(), Schema: auth.Catalog.Get(original.Namespace()).Name(), Ref: ref, Columns: columns}
	return &analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{qualifier}, Frame: original.Columns}
}

func (e *Engine) bindAlterTypeUsingLocked(scope *StatementScope, auth AuthorizationView, original *catalog.Relation, ref ids.ObjectRef, action parser.AlterTableAction, usage sqltype.Usage, namespace *analyze.QueryNamespace) (expr.Expr, []catalog.ExpressionDependency, error) {
	if action.Using == nil {
		source := original.Index[action.Column.Text]
		return plan.ColumnExpr{Info: expr.Info{Usage: original.Columns[source].Usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: source}.WithGeneration(original, source, 0), nil, nil
	}
	if namespace == nil {
		namespace = alterTypeNamespace(auth, original, ref)
	}
	binder := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
	binder.Target.Relation, binder.Target.Ref, binder.Params.NoParameters, binder.Query.Auth = original, ref, true, &auth
	binder.Query.Namespace = namespace
	binder.Query.Sources = []exec.Leaf{{Ref: ref, Relation: original}}
	node, err := binder.Bind(action.Using, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: usage.Type.Usage()})
	if err == nil && (binder.Prepared.RegclassConcat || binder.Consumer.CatalogSnapshot) {
		err = e.captureCommentSnapshotLocked(scope)
	}
	return node, binder.Dependencies, err
}

// Complete phase-one TYPE work against the immutable original row. The caller
// holds Engine.mu; neither row evaluation nor candidate mutation belongs here.
// suppliedUsage is used only by typed propagation of an already resolved type.
func (e *Engine) prepareAlterTypeLocked(scope *StatementScope, op *Operation, auth AuthorizationView, original *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, suppliedUsage *sqltype.Usage) (alterTypePreparation, error) {
	prepared, err := e.admitAlterTypeLocked(scope, op, auth, original, ref, a, suppliedUsage)
	if err != nil {
		return alterTypePreparation{}, err
	}
	return e.completeAlterTypeLocked(scope, auth, original, a, prepared)
}

// Admission resolves the target and binds USING once, before sequence AS.
// Its raw expression is replaced by the completed storage transformation.
func (e *Engine) admitAlterTypeLocked(scope *StatementScope, op *Operation, auth AuthorizationView, original *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, suppliedUsage *sqltype.Usage) (alterTypePreparation, error) {
	fail := func(err error) (alterTypePreparation, error) {
		return alterTypePreparation{}, err
	}
	source, err := alterTypeColumn(original, a.Column.Text)
	if err != nil {
		return fail(err)
	}
	if ref.Epoch != e.catalog.state.Epoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	if _, generated := original.ColumnMeta.Generations[original.Attributes[source]]; generated {
		if a.Using != nil {
			diagnostic := expr.ErrorAt("42611", "cannot specify USING when altering type of generated column", a.Column.Pos).(*sqlstate.Error)
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
	if original.RelationKind == catalog.CompositeRelation {
		return prepared, nil
	}
	if original.RelationKind == catalog.ForeignRelation {
		if a.Using != nil {
			if _, _, err := e.bindAlterTypeUsingLocked(scope, auth, original, ref, a, usage, nil); err != nil {
				return fail(err)
			}
			return fail(sqlError("42809", fmt.Sprintf(`"%s" is not a table`, original.Name())))
		}
		return prepared, nil
	}
	node, dependencies, err := e.bindAlterTypeUsingLocked(scope, auth, original, ref, a, usage, nil)
	if err != nil {
		return fail(err)
	}
	prepared.transformation = columnTransformation{attribute: original.Attributes[source], expression: node, dependencies: dependencies}
	return prepared, nil
}

// Engine.mu remains held. No expression is evaluated during completion.
func (e *Engine) completeAlterTypeLocked(scope *StatementScope, auth AuthorizationView, original *catalog.Relation, a parser.AlterTableAction, prepared alterTypePreparation) (alterTypePreparation, error) {
	fail := func(err error) (alterTypePreparation, error) {
		return alterTypePreparation{}, err
	}
	if original.RelationKind == catalog.CompositeRelation || original.RelationKind == catalog.ForeignRelation {
		return prepared, nil
	}
	usage := prepared.usage
	node := prepared.transformation.expression
	input, annotations := analyze.DomainCoercionInput(node, usage)
	info, err := analyze.StorageCoercionInfo(auth.Catalog, expr.Info{Usage: usage, Property: input.Properties()}, input)
	if err != nil {
		return fail(err)
	}
	coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
	if err != nil {
		if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" {
			d = sqlError("42804", fmt.Sprintf(`column "%s" cannot be cast automatically to type %s`, a.Column.Text, usage.Type.DiagnosticName())).(*sqlstate.Error)
			declaration, _ := usage.Declaration()
			if usage.Type.ID() == sqltype.VarChar || usage.Type.ID() == sqltype.BPChar {
				declaration = usage.Type.DiagnosticName() + strings.TrimPrefix(declaration, usage.Type.SQLName())
			}
			identifier := a.Column.Text
			if parser.IdentifierNeedsQuotes(identifier) {
				identifier = catalog.QuoteIdentifier(identifier)
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
	if coercionPlan.RequiresCatalogOutput() {
		if err := e.captureCommentSnapshotLocked(scope); err != nil {
			return fail(err)
		}
	}
	info.Property = analyze.CoercionProperties(input, coercionPlan)
	node = analyze.RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
	node, err = analyze.StorageDestination(node, usage, info.Pos)
	if err != nil {
		return fail(err)
	}
	source := original.Index[a.Column.Text]
	oldUsage := original.Columns[source].Usage
	prepared.transformation.expression = node
	prepared.transformation.rewrite = a.Using != nil || oldUsage.Type != usage.Type || oldUsage.Typmod != usage.Typmod
	return prepared, nil
}

// Candidate-only mutation consumes the completed phase-one result.
func (e *Engine) alterTypeCandidate(scope *StatementScope, op *Operation, auth AuthorizationView, stage *CatalogStage, original, next *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, prepared alterTypePreparation) (*catalog.Relation, columnTransformation, error) {
	fail := func(err error) (*catalog.Relation, columnTransformation, error) {
		return nil, columnTransformation{}, err
	}
	slot, err := alterTypeColumn(next, a.Column.Text)
	if err != nil {
		return fail(err)
	}
	for _, attribute := range next.Attributes {
		definition, generated := next.ColumnMeta.Generations[attribute]
		if !generated {
			continue
		}
		for _, dependency := range definition.Dependencies {
			if dependency.Address.ClassID == next.Address().ClassID && dependency.Address.ObjectID == next.Address().ObjectID && dependency.Address.SubID == next.Attributes[slot] {
				diagnostic := sqlError("0A000", "cannot alter type of a column used by a generated column").(*sqlstate.Error)
				diagnostic.Detail = fmt.Sprintf(`Column "%s" is used by generated column "%s".`, a.Column.Text, next.Columns[next.IndexForAttribute(attribute)].Name)
				return fail(diagnostic)
			}
		}
	}
	source := original.Index[a.Column.Text]
	oldUsage := original.Columns[source].Usage
	current := next.Columns[slot].Usage
	if current.Type.ID() != oldUsage.Type.ID() || current.Typmod != oldUsage.Typmod {
		return fail(sqlError("0A000", fmt.Sprintf(`cannot alter type of column "%s" twice`, a.Column.Text)))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Epoch != e.catalog.state.Epoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	target := ref.Address
	target.SubID = original.Attributes[source]
	if err := policyTypeChangeError(auth.Catalog, target); err != nil {
		return fail(err)
	}
	if path, pathErr := e.lookupPath(auth, analyze.RelationLookup); pathErr != nil {
		return fail(pathErr)
	} else if dependents := ruleDependentLabels(auth.Catalog, []ids.ObjectAddress{target}, path); len(dependents) != 0 {
		d := sqlError("0A000", "cannot alter type of a column used by a view or rule").(*sqlstate.Error)
		d.Detail = fmt.Sprintf(`%s depends on column "%s"`, dependents[0].label, a.Column.Text)
		return fail(d)
	}
	if edges := viewDependencyClosure(auth.Catalog, []ids.ObjectAddress{target}, stage); len(edges) != 0 {
		view := edges[0].view
		name := view.Name()
		if parser.IdentifierNeedsQuotes(name) {
			name = catalog.QuoteIdentifier(name)
		}
		namespace := auth.Catalog.Get(view.Namespace()).Name()
		path, err := e.lookupPath(auth, analyze.RelationLookup)
		if err != nil {
			return fail(err)
		}
		visible := false
		for _, schema := range path {
			found := auth.Catalog.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: schema, Name: view.Name()})
			if len(found) != 0 {
				visible = len(found) == 1 && found[0] == view.Address()
				break
			}
		}
		if !visible {
			if parser.IdentifierNeedsQuotes(namespace) {
				namespace = catalog.QuoteIdentifier(namespace)
			}
			name = namespace + "." + name
		}
		d := sqlError("0A000", "cannot alter type of a column used by a view or rule").(*sqlstate.Error)
		d.Detail = fmt.Sprintf(`rule _RETURN on view %s depends on column "%s"`, name, a.Column.Text)
		return fail(d)
	}
	usage := prepared.usage
	if original.RelationKind == catalog.CompositeRelation {
		if usage == current && next.Columns[slot].DeclaredDimensions == prepared.declaredDimensions {
			return next, columnTransformation{}, nil
		}
		replacement := *next
		replacement.Columns = append([]catalog.Column(nil), next.Columns...)
		replacement.Columns[slot].Usage = usage
		replacement.Columns[slot].DeclaredDimensions = prepared.declaredDimensions
		return &replacement, columnTransformation{}, nil
	}
	attribute := original.Attributes[source]
	transformation := prepared.transformation
	dimensions := prepared.declaredDimensions
	if usage == current && next.Columns[slot].DeclaredDimensions == dimensions {
		return next, transformation, nil
	}
	replacement := *next
	replacement.Columns = append([]catalog.Column(nil), next.Columns...)
	replacement.Columns[slot].Usage = usage
	replacement.Columns[slot].DeclaredDimensions = dimensions
	if usage == current {
		return &replacement, transformation, nil
	}
	if definition, exists := next.ColumnMeta.Defaults[attribute]; exists {
		expression := definition.Expression
		for {
			wrapper, ok := expression.(plan.CoercionExpr)
			if !ok || wrapper.Mode == sqltype.Explicit {
				break
			}
			expression = wrapper.Child
		}
		input, annotations := analyze.DomainCoercionInput(expression, usage)
		info, err := analyze.StorageCoercionInfo(auth.Catalog, expr.Info{Usage: usage, Property: input.Properties()}, input)
		if err != nil {
			return fail(err)
		}
		defaultPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if err != nil {
			return fail(sqlError("42804", fmt.Sprintf(`default for column "%s" cannot be cast automatically to type %s`, a.Column.Text, usage.Type.DiagnosticName())))
		}
		info.Property = analyze.CoercionProperties(input, defaultPlan)
		expression = analyze.RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: defaultPlan, Mode: sqltype.Assignment})
		expression, err = analyze.StorageDestination(expression, usage, info.Pos)
		if err != nil {
			return fail(err)
		}
		definition.Dependencies = nil
		definition.CatalogSnapshot = false
		_, err = (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.Owner, roles: auth.Roles}, dependencies: &definition.Dependencies, commentSnapshot: &definition.CatalogSnapshot}).sql(op.Context(), auth.Catalog, expression)
		if err != nil {
			return fail(err)
		}
		replacement.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, len(next.ColumnMeta.Defaults))
		for id, d := range next.ColumnMeta.Defaults {
			replacement.ColumnMeta.Defaults[id] = d
		}
		definition.Expression = expression
		definition.OID = 0
		replacement.ColumnMeta.Defaults[attribute] = definition
	}
	if err := e.captureCheckCommentSnapshotLocked(scope, &replacement); err != nil {
		return fail(err)
	}
	return &replacement, transformation, nil
}

// Rewrite only effective live heads. Own updates become inserts into the private
// replacement because their committed predecessors are intentionally omitted.
func rewriteTypeRows(scope *StatementScope, op *Operation, old, next *catalog.Relation, store *storage.Relation, frontier ids.CommitSeq, transforms []columnTransformation, additions []addedColumn, generations []int32, validator alterRowValidator) (*storage.Relation, []storage.RowVersion, *visibility.PrivateDeletes, error) {
	if store.Generation == ^uint64(0) {
		return nil, nil, nil, fmt.Errorf("stuntgres: storage generation exhausted")
	}
	slots := make([]int, len(next.Attributes))
	destinations := make([]int, len(transforms))
	addedSlots := make([]struct {
		index  int
		perRow bool
	}, len(additions))
	generatedSlots := make([]int, len(generations))
	for i, attribute := range generations {
		generatedSlots[i] = next.IndexForAttribute(attribute)
	}
	for i, added := range additions {
		addedSlots[i].index = next.Index[added.column.Name]
		if added.expression != nil {
			properties := added.expression.Properties()
			addedSlots[i].perRow = properties.Volatility > expr.Stable || properties.RuntimeDomainConversion
		}
	}
	for i, attribute := range next.Attributes {
		slots[i] = -1
		for j, id := range old.Attributes {
			if id == attribute {
				slots[i] = j
				break
			}
		}
	}
	for i, transform := range transforms {
		for slot, attribute := range next.Attributes {
			if transform.attribute == attribute {
				destinations[i] = slot
				slots[slot] = -1
				break
			}
		}
	}
	writes, deletes := scope.columnProjectionRoots(old.Address())
	var replaced map[ids.RowID]bool
	for _, row := range writes {
		if row.Base != 0 {
			if replaced == nil {
				replaced = make(map[ids.RowID]bool)
			}
			replaced[row.Row] = true
		}
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: scope.routineAuth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	preparedTransforms := false
	var generationProgram []expr.Expr
	preparedGeneration := false
	convert := func(input []storage.RowVersion, committed bool) ([]storage.RowVersion, error) {
		result := make([]storage.RowVersion, 0, len(input))
		for _, row := range input {
			if err := op.Context().Err(); err != nil {
				return nil, err
			}
			if committed && replaced[row.Row] || !visibility.LiveVersion(row, frontier, store.Ends, deletes) {
				continue
			}
			evaluation.Row = row.Values
			if !preparedTransforms {
				var private []columnTransformation
				for i, transform := range transforms {
					recipe := evaluation
					recipe.RoutineRecipe, recipe.RoutineGraft = true, false
					recipe.RoutineChecks = transform.dependencies
					node, changed, err := preflightCoercionsWithPolicy(&recipe, transform.expression, true)
					evaluation.PreparedDomainChecks = recipe.PreparedDomainChecks
					if err != nil {
						return nil, err
					}
					if err := validateExpressionJoinExecution(&recipe, node, recipe.Catalog); err != nil {
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
					values[i] = row.Values[slot]
				}
			}
			for i, transform := range transforms {
				// Lazy SQL bodies still need this transformation's original
				// manifest after preparation has returned.
				recipe := evaluation
				recipe.RoutineRecipe, recipe.RoutineGraft = true, false
				recipe.RoutineChecks = transform.dependencies
				v, err := transform.expression.Eval(&recipe)
				evaluation.PreparedDomainChecks = recipe.PreparedDomainChecks
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
				expression := generationRecipeExpression(next.ColumnMeta.Generations[attribute])
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
			row.Values = values
			row.IndexOutputs = nil
			if !committed {
				row.Base = 0
			}
			result = append(result, row)
		}
		return result, nil
	}
	committed, err := convert(store.Versions, true)
	if err != nil {
		return nil, nil, nil, err
	}
	own, err := convert(writes, false)
	if err != nil {
		return nil, nil, nil, err
	}
	return &storage.Relation{Versions: committed, Generation: store.Generation + 1}, own, nil, nil
}
