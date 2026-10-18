package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Generation definitions have stable attribute identities, separate from defaults.
type columnGeneration struct {
	OID        ids.OID
	kind       generationKind
	expression BoundExpr
	// Retain analyzed syntax for dependencies/deparse and its physical plan for writes.
	planned         BoundExpr
	dependencies    []ids.ObjectRef
	catalogSnapshot bool
}

type generationKind uint8

const (
	generationStored generationKind = iota + 1
	generationVirtual
)

func (k generationKind) sql() string {
	if k == generationVirtual {
		return "VIRTUAL"
	}
	return "STORED"
}

type tableOIDExpression struct {
	expr.Info
	owner ids.ObjectRef
}

func (n tableOIDExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
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

func (e *Engine) bindCreateGenerations(op *Operation, auth AuthorizationView, d *relationDescriptor, declarations []createColumnDeclaration, input sqltype.InputEnvironment) error {
	for slot, declaration := range declarations {
		if declaration.generation != nil {
			if d.generations == nil {
				d.generations = make(map[int32]columnGeneration)
			}
			kind := generationVirtual
			if declaration.generation.Kind == "stored" {
				kind = generationStored
			}
			d.generations[d.attributes[slot]] = columnGeneration{kind: kind}
		}
	}
	if len(d.generations) == 0 {
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
		d.generations[d.attributes[slot]] = definition
	}
	return nil
}

func (e *Engine) generationNamespace(auth AuthorizationView, d *relationDescriptor) queryNamespace {
	ref := e.catalog.domain.Ref(auth.catalog.Epoch(), d.address)
	columns := make([]queryColumn, len(d.columns))
	for i, c := range d.columns {
		columns[i] = queryColumn{descriptor: c, slot: i, leaf: 0, physical: i}
	}
	qualifier := queryQualifier{name: d.name, original: d.name, schema: auth.catalog.Get(d.namespace).Name(), ref: ref, columns: columns}
	return queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: d.columns}
}

// CREATE and ALTER share binding, security, coercion and provider capture.
func (e *Engine) bindGeneration(op *Operation, auth AuthorizationView, d *relationDescriptor, slot int, expression parser.Expr, input sqltype.InputEnvironment, ns *queryNamespace) (columnGeneration, error) {
	b := newExpressionBinder(op.Context(), e, input, op, nil)
	b.generatedOwner = d
	b.noParameters = true
	b.queryAuth = &auth
	b.queryNamespace = ns
	ref := e.catalog.domain.Ref(auth.catalog.Epoch(), d.address)
	b.querySources = []queryLeaf{{ref: ref, relation: d}}
	column := d.columns[slot]
	kind := d.generations[d.attributes[slot]].kind
	if kind == generationVirtual {
		if err := virtualDestination(column); err != nil {
			return columnGeneration{}, err
		}
	}
	node, err := b.bind(expression, expressionPolicy{context: projectionContext, expected: column.Usage.Type.Usage()})
	if err != nil {
		return columnGeneration{}, err
	}
	analyzed := node
	if kind == generationVirtual {
		if err := virtualGenerationSecurity(analyzed); err != nil {
			return columnGeneration{}, err
		}
	}
	evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: b.input, catalog: auth.catalog, routineAuthority: &auth, routineRecipe: true, routineChecks: b.dependencies}
	node, _, err = simplifyConditional(&evaluation, node, true)
	if err != nil {
		return columnGeneration{}, err
	}
	if !generationImmutable(node) {
		return columnGeneration{}, sqlError("42P17", "generation expression is not immutable")
	}
	if node.ResultUsage() != column.Usage {
		input, annotations := domainCoercionInput(node, column.Usage)
		info, failure := storageCoercionInfo(b.bindingCatalog(), b.info(column.Usage, parser.Position{}, input.Properties()), input)
		if failure != nil {
			return columnGeneration{}, failure
		}
		plan, failure := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if failure != nil {
			diagnostic := sqlError("42804", fmt.Sprintf(`column "%s" is of type %s but default expression is of type %s`, column.Name, column.Usage.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName())).(*sqlstate.Error)
			diagnostic.Hint = "You will need to rewrite or cast the expression."
			return columnGeneration{}, diagnostic
		}
		info.Property = coercionProperties(input, plan)
		node = restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, sqltype.Assignment, false})
		analyzedInput, analyzedAnnotations := domainCoercionInput(analyzed, column.Usage)
		analyzedInfo, failure := storageCoercionInfo(b.bindingCatalog(), b.info(column.Usage, parser.Position{}, analyzedInput.Properties()), analyzedInput)
		if failure != nil {
			return columnGeneration{}, failure
		}
		analyzedPlan, failure := sqltype.PlanCoercion(analyzedInput.ResultUsage(), analyzedInfo.Usage, sqltype.Assignment)
		if failure != nil {
			return columnGeneration{}, failure
		}
		analyzedInfo.Property = coercionProperties(analyzedInput, analyzedPlan)
		analyzed = restoreDomainCastCollations(analyzedAnnotations, coercionExpression{analyzedInfo, analyzedInput, analyzedPlan, sqltype.Assignment, false})
	}
	node, err = storageDestination(node, column.Usage, parser.Position{})
	if err != nil {
		return columnGeneration{}, err
	}
	analyzed, err = storageDestination(analyzed, column.Usage, parser.Position{})
	if err != nil {
		return columnGeneration{}, err
	}
	definition := columnGeneration{kind: kind, expression: analyzed, planned: node}
	_, err = (scalarRenderer{definition: op.definition, aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: d, dependencies: &definition.dependencies, commentSnapshot: &definition.catalogSnapshot}).sql(op.Context(), auth.catalog, analyzed)
	return definition, err
}

// Walk surviving providers, not conservative properties retained on CASE wrappers.
func generationImmutable(node BoundExpr) bool {
	if node == nil {
		return true
	}
	one := generationImmutable
	both := func(a, b BoundExpr) bool { return one(a) && one(b) }
	args := func(nodes []BoundExpr) bool {
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
		var storage [13]BoundExpr
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
		return n.procedure != nil && n.procedure.volatility == Immutable && args(n.Args)
	case installedFunctionExpression:
		return n.procedure != nil && n.procedure.volatility == Immutable && args([]BoundExpr{n.Arg})
	case sqlJSONExpression:
		return n.Property.Volatility == Immutable && args(n.args)
	case jsonPredicateExpression:
		return args([]BoundExpr{n.child})
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence. A
		// provider is admitted by its own volatility and its children.
		if intrinsic, ok := n.(expr.Intrinsic); ok {
			return intrinsic.IntrinsicVolatility() == Immutable && args(n.Children())
		}
	}
	return node.Properties().Volatility == Immutable && !node.Properties().StageDependent && !node.Properties().ParameterDependent
}

func generationRecipeExpression(definition columnGeneration) BoundExpr {
	if definition.expression != nil {
		return definition.expression
	}
	// Standalone internal provider fixtures may supply only an executable.
	// Published SQL generation descriptors require the declaration expression.
	return definition.planned
}

// One execution owner uses one fixed generation program for this descriptor.
// ALTER borrows its existing selected-slot vector; ordinary writes demand all
// stored slots. Neither path admits unused recipes or checks rights per row.
func prepareGenerationProgram(c *EvalContext, d *relationDescriptor, selected []int, consumer *relationDescriptor) ([]BoundExpr, error) {
	program, prepared := c.preparedGenerations[d]
	if prepared {
		return program, nil
	}
	stored := false
	count := len(d.attributes)
	if selected != nil {
		count = len(selected)
	}
	for i := range count {
		slot := i
		if selected != nil {
			slot = selected[i]
		}
		definition, exists := d.generations[d.attributes[slot]]
		if !exists || definition.kind != generationStored {
			continue
		}
		stored = true
		recipe := *c
		recipe.routineRecipe, recipe.routineGraft = true, false
		recipe.routineChecks = nil
		recipe.routineReferences = definition.dependencies
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
				program = make([]BoundExpr, len(d.columns))
				for i, attribute := range d.attributes {
					program[i] = generationRecipeExpression(d.generations[attribute])
				}
			}
			program[slot] = node
		}
	}
	if stored {
		if c.preparedGenerations == nil {
			c.preparedGenerations = make(map[*relationDescriptor][]BoundExpr)
		}
		c.preparedGenerations[d] = program
	}
	return program, nil
}

func evaluateGenerations(ctx context.Context, d *relationDescriptor, row []sqltype.Value, c *EvalContext) error {
	if len(d.generations) == 0 {
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
	for slot, attribute := range d.attributes {
		definition, ok := d.generations[attribute]
		if !ok {
			continue
		}
		if definition.kind == generationVirtual {
			row[slot] = sqltype.Null(d.columns[slot].Usage.Type)
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

func (d *relationDescriptor) indexForAttribute(attribute int32) int {
	for slot, live := range d.attributes {
		if live == attribute {
			return slot
		}
	}
	return -1
}

func generationHasTableOID(node BoundExpr) bool {
	if node == nil {
		return false
	}
	one := generationHasTableOID
	both := func(a, b BoundExpr) bool { return one(a) || one(b) }
	args := func(nodes []BoundExpr) bool {
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
		var storage [13]BoundExpr
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
		return args([]BoundExpr{n.Arg})
	case sqlJSONExpression:
		return args(n.args)
	case jsonPredicateExpression:
		return args([]BoundExpr{n.child})
	case expr.Composite:
		return args(n.Children())
	default:
		return false
	}
}

// Replacement candidates own detached vectors; the source and retained TEMP
// roots are never modified. Only owner-dependent stored cells are recomputed.
func recomputeRemappedGenerations(ctx context.Context, next *catalogState, storage map[ids.ObjectAddress]*relationStorage, mapping map[ids.ObjectAddress]ids.ObjectAddress, eval EvalContext, changed map[ids.ObjectAddress]map[int32]bool) error {
	for source, target := range mapping {
		if source == target {
			continue
		}
		descriptor, ok := next.Relation(target)
		if !ok || len(descriptor.generations) == 0 {
			continue
		}
		filtered := *descriptor
		filtered.generations = nil
		for attribute, definition := range descriptor.generations {
			if definition.kind != generationStored || !generationHasTableOID(definition.planned) {
				continue
			}
			if filtered.generations == nil {
				filtered.generations = make(map[int32]columnGeneration)
			}
			filtered.generations[attribute] = definition
		}
		if len(filtered.generations) == 0 {
			continue
		}
		rows := storage[target]
		if rows == nil {
			return fmt.Errorf("stuntgres: missing generated replacement storage")
		}
		schema := next.Get(descriptor.namespace).Name()
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
			comparisons = make(map[int]sqltype.Grouping, len(filtered.generations))
			for attribute := range filtered.generations {
				if _, tracked := changed[target][attribute]; !tracked {
					continue
				}
				slot := slices.Index(descriptor.attributes, attribute)
				if slot < 0 {
					return ErrCatalogReplaced
				}
				comparison, err := sqltype.BindGrouping(descriptor.columns[slot].Usage)
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
				column := descriptor.columns[slot]
				value, err := logicalColumnValue(descriptor, slot, row, &eval)
				if err != nil {
					return err
				}
				if value.IsNull() {
					detail, err := failingRowDetail(row, eval.Output, nil, nil, descriptor)
					if err != nil {
						return err
					}
					return notNullDetailError(schema, descriptor.name, column.Name, detail)
				}
			}
			constraint, err := validateCheckRow(historical, row, &eval)
			if err != nil {
				return err
			}
			if constraint != nil {
				return checkDetailError(schema, descriptor.name, constraint.Name, "")
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
					changed[target][descriptor.attributes[slot]] = true
				}
			}
			rows.versions[i].values = row
			rows.versions[i].indexOutputs = nil
		}
	}
	return ctx.Err()
}

func validateGenerationProviders(view CatalogRead, d *relationDescriptor) error {
	for _, definition := range d.generations {
		for _, ref := range definition.dependencies {
			address := ref.Address
			address.SubID = 0
			if !storedReferenceEpochValid(view, d, ref) {
				return fmt.Errorf("stuntgres: missing generation provider")
			}
			if address == d.address && ref.Address.SubID != 0 {
				if _, generated := d.generations[ref.Address.SubID]; generated {
					return fmt.Errorf("stuntgres: generated dependency on generated attribute")
				}
			}
			if address == d.address {
				if ref.Address.SubID != 0 && !d.hasAttribute(ref.Address.SubID) {
					return fmt.Errorf("stuntgres: missing generation source attribute")
				}
				continue
			}
			provider, ok := view.Descriptor(address)
			relation, relationProvider := provider.(*relationDescriptor)
			if !ok || provider.Kind() != ids.TypeObject && provider.Kind() != ids.CollationObject && provider.Kind() != ids.OperatorObject && provider.Kind() != ids.NamespaceObject && provider.Kind() != ids.TSDictionaryObject && provider.Kind() != ids.ProcedureObject && (!relationProvider || ref.Address.SubID != 0 || relation.address.ClassID != ids.RelationClassID) || provider.Kind() == ids.NamespaceObject && ref.Address.SubID != 0 {
				return fmt.Errorf("stuntgres: invalid generation provider")
			}
			if provider.Kind() == ids.ProcedureObject {
				// Analyzed dependencies retain installed identity even when a
				// branch is pruned. Surviving-tree immutability is checked
				// separately; validate the actual procedure manifest here.
				procedure, ok := provider.(*procedureDescriptor)
				if !ok || ref.Address.SubID != 0 {
					return fmt.Errorf("stuntgres: invalid generation procedure")
				}
				if err := (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, procedure); err != nil {
					return err
				}
			}
			if provider.Kind() == ids.CollationObject {
				if err := validateStoredCollationDependency(view, ref, definition.expression); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// The analyzed tree supplies provider-signature validation. The physical tree
// independently validates its concrete usages and row/owner identities.
func validateGenerationPlan(view CatalogRead, d *relationDescriptor, node BoundExpr) error {
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
	child := func(n BoundExpr) error { return validateGenerationPlan(view, d, n) }
	pair := func(a, b BoundExpr) error {
		if err := child(a); err != nil {
			return err
		}
		return child(b)
	}
	args := func(nodes []BoundExpr) error {
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
		var storage [13]BoundExpr
		return args(subscriptChildren(n, &storage))
	case columnExpression:
		if _, generated := d.generations[n.origin.Address.SubID]; generated || n.virtual != nil {
			return fmt.Errorf("stuntgres: generated reference in generation plan")
		}
		if !storedReferenceEpochValid(view, d, n.origin) || n.origin.Address.ClassID != d.address.ClassID || n.origin.Address.ObjectID != d.address.ObjectID || n.scope != 1 || n.frame != 1 || n.slot < 0 || n.slot >= len(d.columns) || d.attributes[n.slot] != n.origin.Address.SubID || usage != d.columns[n.slot].Usage {
			return fmt.Errorf("stuntgres: invalid generation plan row")
		}
		return nil
	case tableOIDExpression:
		if !storedReferenceEpochValid(view, d, n.owner) || n.owner.Address != d.address || usage.Type.ID() != sqltype.Oid {
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
		return args([]BoundExpr{n.Arg})
	case sqlJSONExpression:
		return args(n.args)
	case jsonPredicateExpression:
		return args([]BoundExpr{n.child})
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

func generationDepends(d *relationDescriptor, target ids.ObjectAddress) bool {
	for _, definition := range d.generations {
		for _, dependency := range definition.dependencies {
			address := dependency.Address
			if address.ClassID == target.ClassID && address.ObjectID == target.ObjectID && (target.SubID == 0 || address.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}
