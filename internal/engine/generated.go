package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type tableOIDExpression struct {
	expr.Info
	owner ids.ObjectRef
}

func (n tableOIDExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.Scope != 1 || c.Frame != 1 || n.owner.Address.ObjectID == 0 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid generated tableoid frame")
	}
	owner := n.owner
	if c.physicalTable != (ids.ObjectRef{}) {
		owner = c.physicalTable
	}
	return sqltype.OIDValue(n.Usage.Type, uint32(owner.Address.ObjectID))
}

func generationWriteError(column string, update bool) error {
	message := fmt.Sprintf(`cannot insert a non-DEFAULT value into column "%s"`, column)
	if update {
		message = fmt.Sprintf(`column "%s" can only be updated to DEFAULT`, column)
	}
	diagnostic := sqlError("428C9", message).(*sqlstate.Error)
	diagnostic.Detail = fmt.Sprintf(`Column "%s" is a generated column.`, column)
	return diagnostic
}

func (e *Engine) bindCreateGenerations(op *Operation, auth AuthorizationView, d *catalog.Relation, declarations []createColumnDeclaration, input sqltype.InputEnvironment) error {
	for slot, declaration := range declarations {
		if declaration.generation != nil {
			if d.Generations == nil {
				d.Generations = make(map[int32]catalog.ColumnGeneration)
			}
			kind := catalog.GenerationVirtual
			if declaration.generation.Kind == "stored" {
				kind = catalog.GenerationStored
			}
			d.Generations[d.Attributes[slot]] = catalog.ColumnGeneration{Kind: kind}
		}
	}
	if len(d.Generations) == 0 {
		return nil
	}
	ns := e.generationNamespace(auth, d)
	for slot, declaration := range declarations {
		if declaration.generation == nil {
			continue
		}
		definition, err := e.bindGeneration(op, auth, d, slot, declaration.generation.Expression, input, &ns)
		if err != nil {
			return err
		}
		d.Generations[d.Attributes[slot]] = definition
	}
	return nil
}

func (e *Engine) generationNamespace(auth AuthorizationView, d *catalog.Relation) queryNamespace {
	ref := e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Address())
	columns := make([]queryColumn, len(d.Columns))
	for i, c := range d.Columns {
		columns[i] = queryColumn{descriptor: c, slot: i, leaf: 0, physical: i}
	}
	qualifier := queryQualifier{name: d.Name(), original: d.Name(), schema: auth.catalog.Get(d.Namespace()).Name(), ref: ref, columns: columns}
	return queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: d.Columns}
}

// CREATE and ALTER share binding, security, coercion and provider capture.
func (e *Engine) bindGeneration(op *Operation, auth AuthorizationView, d *catalog.Relation, slot int, expression parser.Expr, input sqltype.InputEnvironment, ns *queryNamespace) (catalog.ColumnGeneration, error) {
	b := newExpressionBinder(op.Context(), e, input, op, nil)
	b.generatedOwner = d
	b.noParameters = true
	b.queryAuth = &auth
	b.queryNamespace = ns
	ref := e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Address())
	b.querySources = []queryLeaf{{ref: ref, relation: d}}
	column := d.Columns[slot]
	kind := d.Generations[d.Attributes[slot]].Kind
	if kind == catalog.GenerationVirtual {
		if err := virtualDestination(column); err != nil {
			return catalog.ColumnGeneration{}, err
		}
	}
	node, err := b.bind(expression, expressionPolicy{context: projectionContext, expected: column.Usage.Type.Usage()})
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	analyzed := node
	if kind == catalog.GenerationVirtual {
		if err := virtualGenerationSecurity(analyzed); err != nil {
			return catalog.ColumnGeneration{}, err
		}
	}
	evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: b.input, catalog: auth.catalog, routineAuthority: &auth, routineRecipe: true, routineChecks: b.dependencies}
	node, _, err = simplifyConditional(&evaluation, node, true)
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	if !generationImmutable(node) {
		return catalog.ColumnGeneration{}, sqlError("42P17", "generation expression is not immutable")
	}
	if node.ResultUsage() != column.Usage {
		input, annotations := domainCoercionInput(node, column.Usage)
		info, failure := storageCoercionInfo(b.bindingCatalog(), b.info(column.Usage, parser.Position{}, input.Properties()), input)
		if failure != nil {
			return catalog.ColumnGeneration{}, failure
		}
		plan, failure := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if failure != nil {
			diagnostic := sqlError("42804", fmt.Sprintf(`column "%s" is of type %s but default expression is of type %s`, column.Name, column.Usage.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName())).(*sqlstate.Error)
			diagnostic.Hint = "You will need to rewrite or cast the expression."
			return catalog.ColumnGeneration{}, diagnostic
		}
		info.Property = coercionProperties(input, plan)
		node = restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, sqltype.Assignment, false})
		analyzedInput, analyzedAnnotations := domainCoercionInput(analyzed, column.Usage)
		analyzedInfo, failure := storageCoercionInfo(b.bindingCatalog(), b.info(column.Usage, parser.Position{}, analyzedInput.Properties()), analyzedInput)
		if failure != nil {
			return catalog.ColumnGeneration{}, failure
		}
		analyzedPlan, failure := sqltype.PlanCoercion(analyzedInput.ResultUsage(), analyzedInfo.Usage, sqltype.Assignment)
		if failure != nil {
			return catalog.ColumnGeneration{}, failure
		}
		analyzedInfo.Property = coercionProperties(analyzedInput, analyzedPlan)
		analyzed = restoreDomainCastCollations(analyzedAnnotations, coercionExpression{analyzedInfo, analyzedInput, analyzedPlan, sqltype.Assignment, false})
	}
	node, err = storageDestination(node, column.Usage, parser.Position{})
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	analyzed, err = storageDestination(analyzed, column.Usage, parser.Position{})
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	definition := catalog.ColumnGeneration{Kind: kind, Expression: analyzed, Planned: node}
	_, err = (scalarRenderer{definition: op.definition, aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: d, dependencies: &definition.Dependencies, commentSnapshot: &definition.CatalogSnapshot}).sql(op.Context(), auth.catalog, analyzed)
	return definition, err
}

// Walk surviving providers, not conservative properties retained on CASE wrappers.
func generationImmutable(node expr.Expr) bool {
	if node == nil {
		return true
	}
	one := generationImmutable
	both := func(a, b expr.Expr) bool { return one(a) && one(b) }
	args := func(nodes []expr.Expr) bool {
		for _, child := range nodes {
			if !one(child) {
				return false
			}
		}
		return true
	}
	switch n := node.(type) {
	case rowExpression:
		return args(n.fields)
	case fieldExpression:
		return one(n.base)
	case arrayExpression:
		return args(n.elements)
	case subscriptExpression:
		var storage [13]expr.Expr
		return args(subscriptChildren(n, &storage))
	case literalExpression, columnExpression, tableOIDExpression:
		return true
	case collationExpression:
		return one(n.child)
	case coercionExpression:
		return one(n.child) && (n.mode == sqltype.Assignment || !n.plan.SessionDependent())
	case selectedOperatorExpression:
		return one(n.provider)
	case arithmeticExpression:
		return !n.provider.SessionDependent() && both(n.left, n.right)
	case bitwiseExpression:
		return both(n.left, n.right)
	case rowComparisonExpression:
		for _, pair := range n.comparisons {
			if pair.predicate.SessionDependent() || !both(pair.left, pair.right) {
				return false
			}
		}
		return true
	case scalarArrayComparisonExpression:
		return !n.predicate.SessionDependent() && one(n.left) && (n.array == nil || one(n.array)) && args(n.items)
	case comparisonExpression:
		return !n.predicate.SessionDependent() && both(n.left, n.right)
	case logicalExpression:
		return both(n.left, n.right)
	case distinctExpression:
		return !n.equality.SessionDependent() && both(n.left, n.right)
	case nullTestExpression:
		return one(n.child)
	case truthTestExpression:
		return one(n.child)
	case likeExpression:
		return both(n.left, n.right)
	case conditionalExpression:
		return !n.comparison.SessionDependent() && args(n.args)
	case caseExpression:
		if !both(n.test, n.otherwise) {
			return false
		}
		for _, arm := range n.arms {
			if !both(arm.condition, arm.result) || n.test != nil && (arm.testPlan.SessionDependent() || arm.equality.SessionDependent()) {
				return false
			}
		}
		return true
	case unnestExpression:
		return false
	case aclValueExpression:
		return n.kind != aclExplode && args(n.arguments())
	case objectAddressExpression:
		return false
	case catalogUserNameExpression, catalogForeignKeysExpression:
		return false
	case transactionIdentityExpression:
		return false
	case commitTimestampExpression:
		return false
	case installedSoundexFunctionExpression:
		return args(n.Args)
	case installedMetaphoneFunctionExpression:
		return args(n.Args)
	case installedLevenshteinFunctionExpression:
		return args(n.Args)
	case installedUUIDOsspFunctionExpression:
		return false
	case installedCryptoFunctionExpression:
		return n.procedure != nil && n.procedure.Volatility == expr.Immutable && args(n.Args)
	case installedFunctionExpression:
		return n.procedure != nil && n.procedure.Volatility == expr.Immutable && args([]expr.Expr{n.Arg})
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence. A
		// provider is admitted by its own volatility and its children.
		if intrinsic, ok := n.(expr.Intrinsic); ok {
			return intrinsic.IntrinsicVolatility() == expr.Immutable && args(n.Children())
		}
	}
	return node.Properties().Volatility == expr.Immutable && !node.Properties().StageDependent && !node.Properties().ParameterDependent
}

func generationRecipeExpression(definition catalog.ColumnGeneration) expr.Expr {
	if definition.Expression != nil {
		return definition.Expression
	}
	// Standalone internal provider fixtures may supply only an executable.
	// Published SQL generation descriptors require the declaration expression.
	return definition.Planned
}

// One execution owner uses one fixed generation program for this descriptor.
// ALTER borrows its existing selected-slot vector; ordinary writes demand all
// stored slots. Neither path admits unused recipes or checks rights per row.
func prepareGenerationProgram(c *EvalContext, d *catalog.Relation, selected []int, consumer *catalog.Relation) ([]expr.Expr, error) {
	program, prepared := c.preparedGenerations[d]
	if prepared {
		return program, nil
	}
	stored := false
	count := len(d.Attributes)
	if selected != nil {
		count = len(selected)
	}
	for i := range count {
		slot := i
		if selected != nil {
			slot = selected[i]
		}
		definition, exists := d.Generations[d.Attributes[slot]]
		if !exists || definition.Kind != catalog.GenerationStored {
			continue
		}
		stored = true
		recipe := *c
		recipe.routineRecipe, recipe.routineGraft = true, false
		recipe.routineChecks = nil
		recipe.routineReferences = definition.Dependencies
		recipe.routineConsumer = consumer
		node, changed, err := preflightCoercionsWithPolicy(&recipe, generationRecipeExpression(definition), true)
		c.preparedDomainChecks = recipe.preparedDomainChecks
		if err != nil {
			return nil, err
		}
		if err := validateExpressionJoinExecution(&recipe, node, recipe.catalog); err != nil {
			return nil, err
		}
		if changed {
			if program == nil {
				program = make([]expr.Expr, len(d.Columns))
				for i, attribute := range d.Attributes {
					program[i] = generationRecipeExpression(d.Generations[attribute])
				}
			}
			program[slot] = node
		}
	}
	if stored {
		if c.preparedGenerations == nil {
			c.preparedGenerations = make(map[*catalog.Relation][]expr.Expr)
		}
		c.preparedGenerations[d] = program
	}
	return program, nil
}

func evaluateGenerations(ctx context.Context, d *catalog.Relation, row []sqltype.Value, c *EvalContext) error {
	if len(d.Generations) == 0 {
		return ctx.Err()
	}
	c.Context = ctx
	c.Scope = 1
	c.Frame = 1
	c.Row = row
	program, err := prepareGenerationProgram(c, d, nil, d)
	if err != nil {
		return err
	}
	for slot, attribute := range d.Attributes {
		definition, ok := d.Generations[attribute]
		if !ok {
			continue
		}
		if definition.Kind == catalog.GenerationVirtual {
			row[slot] = sqltype.Null(d.Columns[slot].Usage.Type)
			continue
		}
		expression := generationRecipeExpression(definition)
		if program != nil {
			expression = program[slot]
		}
		value, err := expression.Eval(c)
		if err != nil {
			return err
		}
		row[slot] = value
	}
	return ctx.Err()
}

func generationHasTableOID(node expr.Expr) bool {
	if node == nil {
		return false
	}
	one := generationHasTableOID
	both := func(a, b expr.Expr) bool { return one(a) || one(b) }
	args := func(nodes []expr.Expr) bool {
		for _, child := range nodes {
			if one(child) {
				return true
			}
		}
		return false
	}
	switch n := node.(type) {
	case rowExpression:
		return args(n.fields)
	case fieldExpression:
		return one(n.base)
	case arrayExpression:
		return args(n.elements)
	case subscriptExpression:
		var storage [13]expr.Expr
		return args(subscriptChildren(n, &storage))
	case tableOIDExpression:
		return true
	case collationExpression:
		return one(n.child)
	case coercionExpression:
		return one(n.child)
	case selectedOperatorExpression:
		return one(n.provider)
	case arithmeticExpression:
		return both(n.left, n.right)
	case bitwiseExpression:
		return both(n.left, n.right)
	case rowComparisonExpression:
		for _, pair := range n.comparisons {
			if both(pair.left, pair.right) {
				return true
			}
		}
		return false
	case scalarArrayComparisonExpression:
		return one(n.left) || n.array != nil && one(n.array) || args(n.items)
	case comparisonExpression:
		return both(n.left, n.right)
	case logicalExpression:
		return both(n.left, n.right)
	case distinctExpression:
		return both(n.left, n.right)
	case nullTestExpression:
		return one(n.child)
	case truthTestExpression:
		return one(n.child)
	case likeExpression:
		return both(n.left, n.right)
	case conditionalExpression:
		return args(n.args)
	case caseExpression:
		if both(n.test, n.otherwise) {
			return true
		}
		for _, arm := range n.arms {
			if both(arm.condition, arm.result) {
				return true
			}
		}
		return false
	case aclValueExpression:
		return args(n.arguments())
	case objectAddressExpression:
		return args(n.args[:])
	case catalogUserNameExpression:
		return generationHasTableOID(n.arg)
	case catalogForeignKeysExpression:
		return false
	case transactionIdentityExpression:
		return n.arg != nil && generationHasTableOID(n.arg)
	case commitTimestampExpression:
		return n.arg != nil && generationHasTableOID(n.arg)
	case installedSoundexFunctionExpression:
		return args(n.Args)
	case installedMetaphoneFunctionExpression:
		return args(n.Args)
	case installedLevenshteinFunctionExpression:
		return args(n.Args)
	case installedUUIDOsspFunctionExpression:
		return false
	case installedCryptoFunctionExpression:
		return args(n.Args)
	case installedFunctionExpression:
		return args([]expr.Expr{n.Arg})
	case expr.Composite:
		return args(n.Children())
	default:
		return false
	}
}

// Replacement candidates own detached vectors; the source and retained TEMP
// roots are never modified. Only owner-dependent stored cells are recomputed.
func recomputeRemappedGenerations(ctx context.Context, next *catalog.State, storage map[ids.ObjectAddress]*relationStorage, mapping map[ids.ObjectAddress]ids.ObjectAddress, eval EvalContext, changed map[ids.ObjectAddress]map[int32]bool) error {
	for source, target := range mapping {
		if source == target {
			continue
		}
		descriptor, ok := next.Relation(target)
		if !ok || len(descriptor.Generations) == 0 {
			continue
		}
		filtered := *descriptor
		filtered.Generations = nil
		for attribute, definition := range descriptor.Generations {
			if definition.Kind != catalog.GenerationStored || !generationHasTableOID(definition.Planned) {
				continue
			}
			if filtered.Generations == nil {
				filtered.Generations = make(map[int32]catalog.ColumnGeneration)
			}
			filtered.Generations[attribute] = definition
		}
		if len(filtered.Generations) == 0 {
			continue
		}
		rows := storage[target]
		if rows == nil {
			return fmt.Errorf("stuntgres: missing generated replacement storage")
		}
		schema := next.Get(descriptor.Namespace()).Name()
		historical := validCheckTraversal(descriptor)
		requiredNotNull := newAlterRowValidator(descriptor, schema).notNull
		if eval.statementScope != nil {
			stage := eval.statementScope.op.session.Engine()
			stage.mu.Lock()
			err := stage.captureCheckCommentSnapshotLocked(eval.statementScope, historical)
			if err == nil {
				err = stage.captureCheckVisibilitySnapshotLocked(eval.statementScope, historical)
			}
			stage.mu.Unlock()
			if err != nil {
				return err
			}
		}
		var comparisons map[int]sqltype.Grouping
		if len(changed[target]) != 0 {
			comparisons = make(map[int]sqltype.Grouping, len(filtered.Generations))
			for attribute := range filtered.Generations {
				if _, tracked := changed[target][attribute]; !tracked {
					continue
				}
				slot := slices.Index(descriptor.Attributes, attribute)
				if slot < 0 {
					return ids.ErrCatalogReplaced
				}
				comparison, err := sqltype.BindGrouping(descriptor.Columns[slot].Usage)
				if err != nil {
					return err
				}
				comparisons[slot] = comparison
			}
		}
		for i := range rows.versions {
			if err := ctx.Err(); err != nil {
				return err
			}
			row := append([]sqltype.Value(nil), rows.versions[i].values...)
			if err := evaluateGenerations(ctx, &filtered, row, &eval); err != nil {
				return err
			}
			for _, slot := range requiredNotNull {
				column := descriptor.Columns[slot]
				value, err := logicalColumnValue(descriptor, slot, row, &eval)
				if err != nil {
					return err
				}
				if value.IsNull() {
					detail, err := failingRowDetail(row, eval.Output, nil, nil, descriptor)
					if err != nil {
						return err
					}
					return notNullDetailError(schema, descriptor.Name(), column.Name, detail)
				}
			}
			constraint, err := validateCheckRow(historical, row, &eval)
			if err != nil {
				return err
			}
			if constraint != nil {
				return checkDetailError(schema, descriptor.Name(), constraint.Name, "")
			}
			for slot, comparison := range comparisons {
				before := rows.versions[i].values[slot]
				if before == row[slot] {
					continue
				}
				equal, err := comparison.Equal(ctx, before, row[slot])
				if err != nil {
					return err
				}
				if !equal {
					if changed[target] == nil {
						changed[target] = make(map[int32]bool)
					}
					changed[target][descriptor.Attributes[slot]] = true
				}
			}
			rows.versions[i].values = row
			rows.versions[i].indexOutputs = nil
		}
	}
	return ctx.Err()
}

func validateGenerationProviders(view catalog.Read, d *catalog.Relation) error {
	for _, definition := range d.Generations {
		for _, ref := range definition.Dependencies {
			address := ref.Address
			address.SubID = 0
			if !storedReferenceEpochValid(view, d, ref) {
				return fmt.Errorf("stuntgres: missing generation provider")
			}
			if address == d.Address() && ref.Address.SubID != 0 {
				if _, generated := d.Generations[ref.Address.SubID]; generated {
					return fmt.Errorf("stuntgres: generated dependency on generated attribute")
				}
			}
			if address == d.Address() {
				if ref.Address.SubID != 0 && !d.HasAttribute(ref.Address.SubID) {
					return fmt.Errorf("stuntgres: missing generation source attribute")
				}
				continue
			}
			provider, ok := view.Descriptor(address)
			relation, relationProvider := provider.(*catalog.Relation)
			if !ok || provider.Kind() != ids.TypeObject && provider.Kind() != ids.CollationObject && provider.Kind() != ids.OperatorObject && provider.Kind() != ids.NamespaceObject && provider.Kind() != ids.TSDictionaryObject && provider.Kind() != ids.ProcedureObject && (!relationProvider || ref.Address.SubID != 0 || relation.Address().ClassID != ids.RelationClassID) || provider.Kind() == ids.NamespaceObject && ref.Address.SubID != 0 {
				return fmt.Errorf("stuntgres: invalid generation provider")
			}
			if provider.Kind() == ids.ProcedureObject {
				// Analyzed dependencies retain installed identity even when a
				// branch is pruned. Surviving-tree immutability is checked
				// separately; validate the actual procedure manifest here.
				procedure, ok := provider.(*catalog.Procedure)
				if !ok || ref.Address.SubID != 0 {
					return fmt.Errorf("stuntgres: invalid generation procedure")
				}
				if err := (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, procedure); err != nil {
					return err
				}
			}
			if provider.Kind() == ids.CollationObject {
				if err := validateStoredCollationDependency(view, ref, definition.Expression); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// The analyzed tree supplies provider-signature validation. The physical tree
// independently validates its concrete usages and row/owner identities.
func validateGenerationPlan(view catalog.Read, d *catalog.Relation, node expr.Expr) error {
	if node == nil {
		return nil
	}
	usage := node.ResultUsage()
	if usage.Type == nil {
		return fmt.Errorf("stuntgres: missing generation plan type")
	}
	if !validExpressionUsage(view, usage) {
		return fmt.Errorf("stuntgres: invalid generation plan type")
	}
	if err := validateUsageCollation(view, usage); err != nil {
		return err
	}
	child := func(n expr.Expr) error { return validateGenerationPlan(view, d, n) }
	pair := func(a, b expr.Expr) error {
		if err := child(a); err != nil {
			return err
		}
		return child(b)
	}
	args := func(nodes []expr.Expr) error {
		for _, n := range nodes {
			if err := child(n); err != nil {
				return err
			}
		}
		return nil
	}
	switch n := node.(type) {
	case rowExpression:
		shape := n.Usage.Type.BaseType()
		if !shape.IsComposite() || len(n.fields) != shape.CompositeLen() || n.presenceSlot < 0 || n.wholeNames == nil && (n.presenceSlot != 0 || n.presenceFrame != 0) || n.wholeNames != nil && (len(n.wholeNames) != len(n.fields) || n.presenceFrame == 0) {
			return fmt.Errorf("stuntgres: invalid composite row plan")
		}
		for i, field := range n.fields {
			if field == nil || !sameRecordUsage(field.ResultUsage(), shape.CompositeMember(i).Usage) {
				return fmt.Errorf("stuntgres: incompatible composite row member")
			}
		}
		return args(n.fields)
	case fieldExpression:
		if n.base == nil || n.base.ResultUsage().Type == nil || !n.base.ResultUsage().Type.BaseType().IsComposite() {
			return fmt.Errorf("stuntgres: invalid composite field base")
		}
		shape := n.base.ResultUsage().Type.BaseType()
		index, ok := shape.CompositeAttributeIndex(n.attribute)
		if !ok || index != n.index || !sameRecordUsage(n.Usage, shape.CompositeMember(index).Usage) {
			return fmt.Errorf("stuntgres: invalid composite field plan")
		}
		return child(n.base)
	case arrayExpression:
		return args(n.elements)
	case subscriptExpression:
		var storage [13]expr.Expr
		return args(subscriptChildren(n, &storage))
	case columnExpression:
		if _, generated := d.Generations[n.origin.Address.SubID]; generated || n.virtual != nil {
			return fmt.Errorf("stuntgres: generated reference in generation plan")
		}
		if !storedReferenceEpochValid(view, d, n.origin) || n.origin.Address.ClassID != d.Address().ClassID || n.origin.Address.ObjectID != d.Address().ObjectID || n.scope != 1 || n.frame != 1 || n.slot < 0 || n.slot >= len(d.Columns) || d.Attributes[n.slot] != n.origin.Address.SubID || usage != d.Columns[n.slot].Usage {
			return fmt.Errorf("stuntgres: invalid generation plan row")
		}
		return nil
	case tableOIDExpression:
		if !storedReferenceEpochValid(view, d, n.owner) || n.owner.Address != d.Address() || usage.Type.ID() != sqltype.Oid {
			return fmt.Errorf("stuntgres: invalid generation plan owner")
		}
		return nil
	case literalExpression:
		if n.value.Type() != usage.Type {
			return fmt.Errorf("stuntgres: invalid generation plan literal")
		}
		return nil
	case collationExpression:
		return child(n.child)
	case coercionExpression:
		return child(n.child)
	case selectedOperatorExpression:
		return child(n.provider)
	case arithmeticExpression:
		return pair(n.left, n.right)
	case bitwiseExpression:
		return pair(n.left, n.right)
	case rowComparisonExpression:
		for _, comparison := range n.comparisons {
			if err := pair(comparison.left, comparison.right); err != nil {
				return err
			}
		}
		return nil
	case scalarArrayComparisonExpression:
		if err := child(n.left); err != nil {
			return err
		}
		if n.array != nil {
			if err := child(n.array); err != nil {
				return err
			}
		}
		return args(n.items)
	case comparisonExpression:
		return pair(n.left, n.right)
	case logicalExpression:
		return pair(n.left, n.right)
	case distinctExpression:
		return pair(n.left, n.right)
	case nullTestExpression:
		return child(n.child)
	case truthTestExpression:
		return child(n.child)
	case likeExpression:
		return pair(n.left, n.right)
	case conditionalExpression:
		return args(n.args)
	case caseExpression:
		if err := pair(n.test, n.otherwise); err != nil {
			return err
		}
		for _, arm := range n.arms {
			if err := pair(arm.condition, arm.result); err != nil {
				return err
			}
		}
		return nil
	case aclValueExpression:
		if err := n.validateStored(); err != nil {
			return err
		}
		return args(n.arguments())
	case objectAddressExpression:
		if err := n.validateStored(); err != nil {
			return err
		}
		return args(n.args[:])
	case catalogUserNameExpression:
		if err := n.validate(); err != nil {
			return err
		}
		return validateGenerationPlan(view, d, n.arg)
	case catalogForeignKeysExpression:
		return n.validate()
	case transactionIdentityExpression:
		if err := n.validate(); err != nil {
			return err
		}
		if n.arg != nil {
			return validateGenerationPlan(view, d, n.arg)
		}
		return nil
	case commitTimestampExpression:
		if err := n.validate(); err != nil {
			return err
		}
		if n.arg != nil {
			return validateGenerationPlan(view, d, n.arg)
		}
		return nil
	case installedSoundexFunctionExpression:
		return args(n.Args)
	case installedMetaphoneFunctionExpression:
		return args(n.Args)
	case installedLevenshteinFunctionExpression:
		return args(n.Args)
	case installedUUIDOsspFunctionExpression:
		return n.validate(view)
	case installedCryptoFunctionExpression:
		return args(n.Args)
	case installedFunctionExpression:
		return args([]expr.Expr{n.Arg})
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence.
		stored, ok := n.(expr.StoredProvider)
		if !ok {
			return fmt.Errorf("stuntgres: unsupported generation plan provider %T", node)
		}
		if err := stored.ValidateStoredProvider(); err != nil {
			return err
		}
		return args(n.Children())
	default:
		return fmt.Errorf("stuntgres: unsupported generation plan provider %T", node)
	}
}

func generationDepends(d *catalog.Relation, target ids.ObjectAddress) bool {
	for _, definition := range d.Generations {
		for _, dependency := range definition.Dependencies {
			address := dependency.Address
			if address.ClassID == target.ClassID && address.ObjectID == target.ObjectID && (target.SubID == 0 || address.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}
