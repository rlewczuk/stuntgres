package engine

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) bindCreateGenerations(op *Operation, auth AuthorizationView, d *catalog.Relation, declarations []createColumnDeclaration, input sqltype.InputEnvironment) error {
	for slot, declaration := range declarations {
		if declaration.Generation != nil {
			if d.ColumnMeta.Generations == nil {
				d.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration)
			}
			kind := catalog.GenerationVirtual
			if declaration.Generation.Kind == "stored" {
				kind = catalog.GenerationStored
			}
			d.ColumnMeta.Generations[d.Attributes[slot]] = catalog.ColumnGeneration{Kind: kind}
		}
	}
	if len(d.ColumnMeta.Generations) == 0 {
		return nil
	}
	ns := e.generationNamespace(auth, d)
	for slot, declaration := range declarations {
		if declaration.Generation == nil {
			continue
		}
		definition, err := e.bindGeneration(op, auth, d, slot, declaration.Generation.Expression, input, &ns)
		if err != nil {
			return err
		}
		d.ColumnMeta.Generations[d.Attributes[slot]] = definition
	}
	return nil
}

func (e *Engine) generationNamespace(auth AuthorizationView, d *catalog.Relation) queryNamespace {
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	columns := make([]queryColumn, len(d.Columns))
	for i, c := range d.Columns {
		columns[i] = queryColumn{descriptor: c, slot: i, leaf: 0, physical: i}
	}
	qualifier := queryQualifier{name: d.Name(), original: d.Name(), schema: auth.Catalog.Get(d.Namespace()).Name(), ref: ref, columns: columns}
	return queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: d.Columns}
}

// CREATE and ALTER share binding, security, coercion and provider capture.
func (e *Engine) bindGeneration(op *Operation, auth AuthorizationView, d *catalog.Relation, slot int, expression parser.Expr, input sqltype.InputEnvironment, ns *queryNamespace) (catalog.ColumnGeneration, error) {
	b := newExpressionBinder(op.Context(), e, input, op, nil)
	b.target.generatedOwner = d
	b.params.noParameters = true
	b.query.auth = &auth
	b.query.namespace = ns
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	b.query.sources = []exec.Leaf{{Ref: ref, Relation: d}}
	column := d.Columns[slot]
	kind := d.ColumnMeta.Generations[d.Attributes[slot]].Kind
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
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: b.input, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.dependencies}}
	node, _, err = simplifyConditional(&evaluation, node, true)
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	if !generationImmutable(node) {
		return catalog.ColumnGeneration{}, sqlError("42P17", "generation expression is not immutable")
	}
	if node.ResultUsage() != column.Usage {
		input, annotations := analyze.DomainCoercionInput(node, column.Usage)
		info, failure := analyze.StorageCoercionInfo(b.bindingCatalog(), b.info(column.Usage, parser.Position{}, input.Properties()), input)
		if failure != nil {
			return catalog.ColumnGeneration{}, failure
		}
		coercionPlan, failure := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if failure != nil {
			diagnostic := sqlError("42804", fmt.Sprintf(`column "%s" is of type %s but default expression is of type %s`, column.Name, column.Usage.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName())).(*sqlstate.Error)
			diagnostic.Hint = "You will need to rewrite or cast the expression."
			return catalog.ColumnGeneration{}, diagnostic
		}
		info.Property = analyze.CoercionProperties(input, coercionPlan)
		node = analyze.RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
		analyzedInput, analyzedAnnotations := analyze.DomainCoercionInput(analyzed, column.Usage)
		analyzedInfo, failure := analyze.StorageCoercionInfo(b.bindingCatalog(), b.info(column.Usage, parser.Position{}, analyzedInput.Properties()), analyzedInput)
		if failure != nil {
			return catalog.ColumnGeneration{}, failure
		}
		analyzedPlan, failure := sqltype.PlanCoercion(analyzedInput.ResultUsage(), analyzedInfo.Usage, sqltype.Assignment)
		if failure != nil {
			return catalog.ColumnGeneration{}, failure
		}
		analyzedInfo.Property = analyze.CoercionProperties(analyzedInput, analyzedPlan)
		analyzed = analyze.RestoreDomainCastCollations(analyzedAnnotations, plan.CoercionExpr{Info: analyzedInfo, Child: analyzedInput, Plan: analyzedPlan, Mode: sqltype.Assignment})
	}
	node, err = analyze.StorageDestination(node, column.Usage, parser.Position{})
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	analyzed, err = analyze.StorageDestination(analyzed, column.Usage, parser.Position{})
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	definition := catalog.ColumnGeneration{Kind: kind, Expression: analyzed, Planned: node}
	_, err = (scalarRenderer{definition: op.definition, aclOutput: capturedACLItemOutput{owner: auth.Owner, roles: auth.Roles}, owner: d, dependencies: &definition.Dependencies, commentSnapshot: &definition.CatalogSnapshot}).sql(op.Context(), auth.Catalog, analyzed)
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
	case plan.RowExpr:
		return args(n.Fields)
	case plan.FieldExpr:
		return one(n.Base)
	case plan.ArrayExpr:
		return args(n.Elements)
	case plan.SubscriptExpr:
		var storage [13]expr.Expr
		return args(exec.SubscriptChildren(n, &storage))
	case plan.LiteralExpr, plan.ColumnExpr, plan.TableOIDExpr:
		return true
	case plan.CollationExpr:
		return one(n.Child)
	case plan.CoercionExpr:
		return one(n.Child) && (n.Mode == sqltype.Assignment || !n.Plan.SessionDependent())
	case plan.SelectedOperatorExpr:
		return one(n.Provider)
	case plan.ArithmeticExpr:
		return !n.Provider.SessionDependent() && both(n.Left, n.Right)
	case plan.BitwiseExpr:
		return both(n.Left, n.Right)
	case plan.RowComparisonExpr:
		for _, pair := range n.Comparisons {
			if pair.Predicate.SessionDependent() || !both(pair.Left, pair.Right) {
				return false
			}
		}
		return true
	case plan.ScalarArrayComparisonExpr:
		return !n.Predicate.SessionDependent() && one(n.Left) && (n.Array == nil || one(n.Array)) && args(n.Items)
	case plan.ComparisonExpr:
		return !n.Predicate.SessionDependent() && both(n.Left, n.Right)
	case plan.LogicalExpr:
		return both(n.Left, n.Right)
	case plan.DistinctExpr:
		return !n.Equality.SessionDependent() && both(n.Left, n.Right)
	case plan.NullTestExpr:
		return one(n.Child)
	case plan.TruthTestExpr:
		return one(n.Child)
	case plan.LikeExpr:
		return both(n.Left, n.Right)
	case plan.ConditionalExpr:
		return !n.Comparison.SessionDependent() && args(n.Args)
	case plan.CaseExpr:
		if !both(n.Test, n.Otherwise) {
			return false
		}
		for _, arm := range n.Arms {
			if !both(arm.Condition, arm.Result) || n.Test != nil && (arm.TestPlan.SessionDependent() || arm.Equality.SessionDependent()) {
				return false
			}
		}
		return true
	case plan.UnnestExpr:
		return false
	case plan.ExtensionPathsExpr:
		return false
	case plan.InstalledSoundexExpr:
		return args(n.Args)
	case plan.InstalledMetaphoneExpr:
		return args(n.Args)
	case plan.InstalledLevenshteinExpr:
		return args(n.Args)
	case plan.InstalledUUIDOsspExpr:
		return n.Procedure != nil && n.Procedure.Volatility == expr.Immutable
	case plan.InstalledCryptoExpr:
		return n.Procedure != nil && n.Procedure.Volatility == expr.Immutable && args(n.Args)
	case plan.InstalledTextExpr:
		return n.Procedure != nil && n.Procedure.Volatility == expr.Immutable && args([]expr.Expr{n.Arg})
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
func prepareGenerationProgram(c *exec.Context, d *catalog.Relation, selected []int, consumer *catalog.Relation) ([]expr.Expr, error) {
	program, prepared := c.PreparedGenerations[d]
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
		definition, exists := d.ColumnMeta.Generations[d.Attributes[slot]]
		if !exists || definition.Kind != catalog.GenerationStored {
			continue
		}
		stored = true
		recipe := *c
		recipe.RoutineRecipe, recipe.RoutineGraft = true, false
		recipe.RoutineChecks = nil
		recipe.RoutineReferences = definition.Dependencies
		recipe.RoutineConsumer = consumer
		node, changed, err := preflightCoercionsWithPolicy(&recipe, generationRecipeExpression(definition), true)
		c.PreparedDomainChecks = recipe.PreparedDomainChecks
		if err != nil {
			return nil, err
		}
		if err := validateExpressionJoinExecution(&recipe, node, recipe.Catalog); err != nil {
			return nil, err
		}
		if changed {
			if program == nil {
				program = make([]expr.Expr, len(d.Columns))
				for i, attribute := range d.Attributes {
					program[i] = generationRecipeExpression(d.ColumnMeta.Generations[attribute])
				}
			}
			program[slot] = node
		}
	}
	if stored {
		if c.PreparedGenerations == nil {
			c.PreparedGenerations = make(map[*catalog.Relation][]expr.Expr)
		}
		c.PreparedGenerations[d] = program
	}
	return program, nil
}

func evaluateGenerations(ctx context.Context, d *catalog.Relation, row []sqltype.Value, c *exec.Context) error {
	if len(d.ColumnMeta.Generations) == 0 {
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
		definition, ok := d.ColumnMeta.Generations[attribute]
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
	case plan.RowExpr:
		return args(n.Fields)
	case plan.FieldExpr:
		return one(n.Base)
	case plan.ArrayExpr:
		return args(n.Elements)
	case plan.SubscriptExpr:
		var storage [13]expr.Expr
		return args(exec.SubscriptChildren(n, &storage))
	case plan.TableOIDExpr:
		return true
	case plan.CollationExpr:
		return one(n.Child)
	case plan.CoercionExpr:
		return one(n.Child)
	case plan.SelectedOperatorExpr:
		return one(n.Provider)
	case plan.ArithmeticExpr:
		return both(n.Left, n.Right)
	case plan.BitwiseExpr:
		return both(n.Left, n.Right)
	case plan.RowComparisonExpr:
		for _, pair := range n.Comparisons {
			if both(pair.Left, pair.Right) {
				return true
			}
		}
		return false
	case plan.ScalarArrayComparisonExpr:
		return one(n.Left) || n.Array != nil && one(n.Array) || args(n.Items)
	case plan.ComparisonExpr:
		return both(n.Left, n.Right)
	case plan.LogicalExpr:
		return both(n.Left, n.Right)
	case plan.DistinctExpr:
		return both(n.Left, n.Right)
	case plan.NullTestExpr:
		return one(n.Child)
	case plan.TruthTestExpr:
		return one(n.Child)
	case plan.LikeExpr:
		return both(n.Left, n.Right)
	case plan.ConditionalExpr:
		return args(n.Args)
	case plan.CaseExpr:
		if both(n.Test, n.Otherwise) {
			return true
		}
		for _, arm := range n.Arms {
			if both(arm.Condition, arm.Result) {
				return true
			}
		}
		return false
	case plan.ExtensionPathsExpr:
		return generationHasTableOID(n.Arg)
	case plan.InstalledSoundexExpr:
		return args(n.Args)
	case plan.InstalledMetaphoneExpr:
		return args(n.Args)
	case plan.InstalledLevenshteinExpr:
		return args(n.Args)
	case plan.InstalledUUIDOsspExpr:
		return false
	case plan.InstalledCryptoExpr:
		return args(n.Args)
	case plan.InstalledTextExpr:
		return args([]expr.Expr{n.Arg})
	case expr.Composite:
		return args(n.Children())
	default:
		return false
	}
}

// Replacement candidates own detached vectors; the source and retained TEMP
// roots are never modified. Only owner-dependent stored cells are recomputed.
func recomputeRemappedGenerations(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress, eval exec.Context, changed map[ids.ObjectAddress]map[int32]bool) error {
	for source, target := range mapping {
		if source == target {
			continue
		}
		descriptor, ok := next.Relation(target)
		if !ok || len(descriptor.ColumnMeta.Generations) == 0 {
			continue
		}
		filtered := *descriptor
		filtered.ColumnMeta.Generations = nil
		for attribute, definition := range descriptor.ColumnMeta.Generations {
			if definition.Kind != catalog.GenerationStored || !generationHasTableOID(definition.Planned) {
				continue
			}
			if filtered.ColumnMeta.Generations == nil {
				filtered.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration)
			}
			filtered.ColumnMeta.Generations[attribute] = definition
		}
		if len(filtered.ColumnMeta.Generations) == 0 {
			continue
		}
		rows := relations[target]
		if rows == nil {
			return fmt.Errorf("stuntgres: missing generated replacement storage")
		}
		schema := next.Get(descriptor.Namespace()).Name()
		historical := validCheckTraversal(descriptor)
		requiredNotNull := newAlterRowValidator(descriptor, schema).notNull
		if scope := scopeOf(eval.Host); scope != nil {
			stage := scope.op.session.Engine()
			stage.mu.Lock()
			err := stage.captureCheckCommentSnapshotLocked(scope, historical)
			if err == nil {
				err = stage.captureCheckVisibilitySnapshotLocked(scope, historical)
			}
			stage.mu.Unlock()
			if err != nil {
				return err
			}
		}
		var comparisons map[int]sqltype.Grouping
		if len(changed[target]) != 0 {
			comparisons = make(map[int]sqltype.Grouping, len(filtered.ColumnMeta.Generations))
			for attribute := range filtered.ColumnMeta.Generations {
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
		for i := range rows.Versions {
			if err := ctx.Err(); err != nil {
				return err
			}
			row := append([]sqltype.Value(nil), rows.Versions[i].Values...)
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
				before := rows.Versions[i].Values[slot]
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
			rows.Versions[i].Values = row
			rows.Versions[i].IndexOutputs = nil
		}
	}
	return ctx.Err()
}

func validateGenerationProviders(view catalog.Read, d *catalog.Relation) error {
	for _, definition := range d.ColumnMeta.Generations {
		for _, ref := range definition.Dependencies {
			address := ref.Address
			address.SubID = 0
			if !storedReferenceEpochValid(view, d, ref) {
				return fmt.Errorf("stuntgres: missing generation provider")
			}
			if address == d.Address() && ref.Address.SubID != 0 {
				if _, generated := d.ColumnMeta.Generations[ref.Address.SubID]; generated {
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
			validRelation := relationProvider && relation.Address().ClassID == ids.RelationClassID
			if validRelation && ref.Address.SubID != 0 {
				rowType, exists := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
				validRelation = exists && compositeAttributeProvider(rowType, relation, ref.Address.SubID)
			}
			if !ok || provider.Kind() != ids.TypeObject && provider.Kind() != ids.CollationObject && provider.Kind() != ids.OperatorObject && provider.Kind() != ids.NamespaceObject && provider.Kind() != ids.TSDictionaryObject && provider.Kind() != ids.ProcedureObject && !validRelation || provider.Kind() == ids.NamespaceObject && ref.Address.SubID != 0 {
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
				if err := (procedureProvider{}).Validate(view, procedure); err != nil {
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
	case plan.RowExpr:
		shape := n.Usage.Type.BaseType()
		if !shape.IsComposite() || n.FieldProjection == nil && len(n.Fields) != shape.CompositeLen() || n.FieldProjection != nil && len(n.FieldProjection) != len(n.Fields) || n.PresenceSlot < 0 || n.WholeNames == nil && (n.PresenceSlot != 0 || n.PresenceFrame != 0) || n.WholeNames != nil && (len(n.WholeNames) != len(n.Fields) || n.PresenceFrame == 0) {
			return fmt.Errorf("stuntgres: invalid composite row plan")
		}
		if n.FieldProjection == nil {
			for i, field := range n.Fields {
				if field == nil || !sqltype.SameRecordUsage(field.ResultUsage(), shape.CompositeMember(i).Usage) {
					return fmt.Errorf("stuntgres: incompatible composite row member")
				}
			}
		} else {
			if n.Raw == nil {
				return fmt.Errorf("stuntgres: retained row projection has no constructor")
			}
			seen := make(map[int32]bool, len(n.FieldProjection))
			live := 0
			for i, projection := range n.FieldProjection {
				if n.Fields[i] == nil || projection.Attribute <= 0 || seen[projection.Attribute] {
					return fmt.Errorf("stuntgres: invalid retained row attribute")
				}
				seen[projection.Attribute] = true
				slot, present := shape.CompositeAttributeIndex(projection.Attribute)
				if !present {
					if projection.Slot != -1 {
						return fmt.Errorf("stuntgres: invalid retired row slot")
					}
					continue
				}
				if slot != projection.Slot || !sqltype.SameRecordUsage(n.Fields[i].ResultUsage(), shape.CompositeMember(slot).Usage) {
					return fmt.Errorf("stuntgres: incompatible retained row member")
				}
				live++
			}
			if live != shape.CompositeLen() {
				return fmt.Errorf("stuntgres: incomplete retained row shape")
			}
		}
		return args(n.Fields)
	case plan.FieldExpr:
		if n.Base == nil || n.Base.ResultUsage().Type == nil || !n.Base.ResultUsage().Type.BaseType().IsComposite() {
			return fmt.Errorf("stuntgres: invalid composite field base")
		}
		shape := n.Base.ResultUsage().Type.BaseType()
		index, ok := shape.CompositeAttributeIndex(n.Attribute)
		source := index
		if row, constructor := n.Base.(plan.RowExpr); constructor {
			source = row.FieldSourceIndex(index)
		}
		if !ok || source != n.Index || !sqltype.SameRecordUsage(n.Usage, shape.CompositeMember(index).Usage) {
			return fmt.Errorf("stuntgres: invalid composite field plan")
		}
		return child(n.Base)
	case plan.ArrayExpr:
		return args(n.Elements)
	case plan.SubscriptExpr:
		var storage [13]expr.Expr
		return args(exec.SubscriptChildren(n, &storage))
	case plan.ColumnExpr:
		if _, generated := d.ColumnMeta.Generations[n.Origin.Address.SubID]; generated || n.Virtual != nil {
			return fmt.Errorf("stuntgres: generated reference in generation plan")
		}
		if !storedReferenceEpochValid(view, d, n.Origin) || n.Origin.Address.ClassID != d.Address().ClassID || n.Origin.Address.ObjectID != d.Address().ObjectID || n.Scope != 1 || n.Frame != 1 || n.Slot < 0 || n.Slot >= len(d.Columns) || d.Attributes[n.Slot] != n.Origin.Address.SubID || usage != d.Columns[n.Slot].Usage {
			return fmt.Errorf("stuntgres: invalid generation plan row")
		}
		return nil
	case plan.TableOIDExpr:
		if !storedReferenceEpochValid(view, d, n.Owner) || n.Owner.Address != d.Address() || usage.Type.ID() != sqltype.Oid {
			return fmt.Errorf("stuntgres: invalid generation plan owner")
		}
		return nil
	case plan.LiteralExpr:
		if n.Value.Type() != usage.Type {
			return fmt.Errorf("stuntgres: invalid generation plan literal")
		}
		return nil
	case plan.CollationExpr:
		return child(n.Child)
	case plan.CoercionExpr:
		return child(n.Child)
	case plan.SelectedOperatorExpr:
		return child(n.Provider)
	case plan.ArithmeticExpr:
		return pair(n.Left, n.Right)
	case plan.BitwiseExpr:
		return pair(n.Left, n.Right)
	case plan.RowComparisonExpr:
		for _, comparison := range n.Comparisons {
			if err := pair(comparison.Left, comparison.Right); err != nil {
				return err
			}
		}
		return nil
	case plan.ScalarArrayComparisonExpr:
		if err := child(n.Left); err != nil {
			return err
		}
		if n.Array != nil {
			if err := child(n.Array); err != nil {
				return err
			}
		}
		return args(n.Items)
	case plan.ComparisonExpr:
		return pair(n.Left, n.Right)
	case plan.LogicalExpr:
		return pair(n.Left, n.Right)
	case plan.DistinctExpr:
		return pair(n.Left, n.Right)
	case plan.NullTestExpr:
		return child(n.Child)
	case plan.TruthTestExpr:
		return child(n.Child)
	case plan.LikeExpr:
		return pair(n.Left, n.Right)
	case plan.ConditionalExpr:
		return args(n.Args)
	case plan.CaseExpr:
		if err := pair(n.Test, n.Otherwise); err != nil {
			return err
		}
		for _, arm := range n.Arms {
			if err := pair(arm.Condition, arm.Result); err != nil {
				return err
			}
		}
		return nil
	case plan.ExtensionPathsExpr:
		if err := n.Validate(); err != nil {
			return err
		}
		return validateGenerationPlan(view, d, n.Arg)
	case plan.InstalledSoundexExpr:
		return args(n.Args)
	case plan.InstalledMetaphoneExpr:
		return args(n.Args)
	case plan.InstalledLevenshteinExpr:
		return args(n.Args)
	case plan.InstalledUUIDOsspExpr:
		return validateInstalledUUIDOssp(n, view)
	case plan.InstalledCryptoExpr:
		return args(n.Args)
	case plan.InstalledTextExpr:
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
	for _, definition := range d.ColumnMeta.Generations {
		for _, dependency := range definition.Dependencies {
			address := dependency.Address
			if address.ClassID == target.ClassID && address.ObjectID == target.ObjectID && (target.SubID == 0 || address.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}
