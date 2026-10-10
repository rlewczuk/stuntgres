package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Generation definitions have stable attribute identities, separate from defaults.
type columnGeneration struct {
	OID        OID
	expression BoundExpr
	// Retain analyzed syntax for dependencies/deparse and its physical plan for writes.
	planned         BoundExpr
	dependencies    []ObjectRef
	catalogSnapshot bool
}

type tableOIDExpression struct {
	expressionInfo
	owner ObjectRef
}

func (n tableOIDExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.Scope != 1 || c.Frame != 1 || n.owner.Address.ObjectID == 0 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid generated tableoid frame")
	}
	return sqltype.OIDValue(n.usage.Type, uint32(n.owner.Address.ObjectID))
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
			d.generations[d.attributes[slot]] = columnGeneration{}
		}
	}
	if len(d.generations) == 0 {
		return nil
	}
	ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: d.address}
	columns := make([]queryColumn, len(d.columns))
	for i, c := range d.columns {
		columns[i] = queryColumn{c, i, 0, i}
	}
	qualifier := queryQualifier{name: d.name, original: d.name, schema: auth.catalog.state.descriptors[d.namespace].Name(), ref: ref, columns: columns}
	ns := queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: d.columns}
	for slot, declaration := range declarations {
		if declaration.generation == nil {
			continue
		}
		b := newExpressionBinder(op.Context(), e, input, op, nil)
		b.generatedOwner = d
		b.noParameters = true
		b.queryAuth = &auth
		b.queryNamespace = &ns
		b.querySources = []queryLeaf{{ref: ref, relation: d}}
		column := d.columns[slot]
		node, err := b.bind(declaration.generation.Expression, expressionPolicy{context: projectionContext, expected: column.Usage.Type.Usage()})
		if err != nil {
			return err
		}
		analyzed := node
		node, _, err = simplifyConditional(&EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: input}, node, true)
		if err != nil {
			return err
		}
		if !generationImmutable(node) {
			return sqlError("42P17", "generation expression is not immutable")
		}
		if node.ResultUsage() != column.Usage {
			plan, failure := sqltype.PlanCoercion(node.ResultUsage(), column.Usage, sqltype.Assignment)
			if failure != nil {
				diagnostic := sqlError("42804", fmt.Sprintf(`column "%s" is of type %s but default expression is of type %s`, column.Name, column.Usage.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName())).(*sqlstate.Error)
				diagnostic.Hint = "You will need to rewrite or cast the expression."
				return diagnostic
			}
			node = coercionExpression{b.info(column.Usage, parser.Position{}, coercionProperties(node, plan)), node, plan, sqltype.Assignment}
			analyzed = coercionExpression{b.info(column.Usage, parser.Position{}, coercionProperties(analyzed, plan)), analyzed, plan, sqltype.Assignment}
		}
		definition := columnGeneration{expression: analyzed, planned: node}
		_, err = (scalarRenderer{owner: d, dependencies: &definition.dependencies, commentSnapshot: &definition.catalogSnapshot}).sql(op.Context(), auth.catalog, analyzed)
		if err != nil {
			return err
		}
		d.generations[d.attributes[slot]] = definition
	}
	return nil
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
	case literalExpression, columnExpression, tableOIDExpression:
		return true
	case collationExpression:
		return one(n.child)
	case coercionExpression:
		return one(n.child) && (n.mode == sqltype.Assignment || !n.plan.SessionDependent())
	case arithmeticExpression:
		return !n.provider.SessionDependent() && both(n.left, n.right)
	case bitwiseExpression:
		return both(n.left, n.right)
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
	case likeEscapeExpression:
		return both(n.pattern, n.escape)
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
	case binaryExpression:
		return args(n.args)
	case datetimeJustifyExpression:
		return args(n.children())
	case datetimeExtractExpression:
		return !n.plan.SessionDependent() && args(n.children())
	case datetimeAgeFiniteExpression:
		return !n.plan.SessionDependent() && args(n.children())
	case datetimeMakeExpression:
		return !n.plan.SessionDependent() && args(n.children())
	case datetimeFormattingExpression:
		return false
	case stringExpression:
		return !n.plan.SessionDependent() && args(n.args)
	case mathExpression:
		return args(n.args)
	case nullCountExpression:
		return args(n.args)
	case formatExpression:
		return false
	default:
		return node.Properties().Volatility == Immutable && !node.Properties().StageDependent && !node.Properties().ParameterDependent
	}
}

func evaluateGenerations(ctx context.Context, d *relationDescriptor, row []sqltype.Value, c *EvalContext) error {
	if len(d.generations) == 0 {
		return ctx.Err()
	}
	c.Context = ctx
	c.Scope = 1
	c.Frame = 1
	c.Row = row
	for slot, attribute := range d.attributes {
		definition, ok := d.generations[attribute]
		if !ok {
			continue
		}
		value, err := definition.planned.Eval(c)
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
	case tableOIDExpression:
		return true
	case collationExpression:
		return one(n.child)
	case coercionExpression:
		return one(n.child)
	case arithmeticExpression:
		return both(n.left, n.right)
	case bitwiseExpression:
		return both(n.left, n.right)
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
	case likeEscapeExpression:
		return both(n.pattern, n.escape)
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
	case binaryExpression:
		return args(n.args)
	case datetimeJustifyExpression:
		return args(n.children())
	case datetimeExtractExpression:
		return args(n.children())
	case datetimeAgeFiniteExpression:
		return args(n.children())
	case datetimeMakeExpression:
		return args(n.children())
	case datetimeFormattingExpression:
		return args(n.args[:])
	case stringExpression:
		return args(n.args)
	case mathExpression:
		return args(n.args)
	case nullCountExpression:
		return args(n.args)
	case formatExpression:
		return args(n.args)
	case timeZoneExpression:
		return both(n.value, n.zone)
	default:
		return false
	}
}

// Replacement candidates own detached vectors; the source and retained TEMP
// roots are never modified. Only owner-dependent stored cells are recomputed.
func recomputeRemappedGenerations(ctx context.Context, next *catalogState, storage map[ObjectAddress]*relationStorage, mapping map[ObjectAddress]ObjectAddress, eval EvalContext) error {
	for source, target := range mapping {
		if source == target {
			continue
		}
		descriptor, ok := next.descriptors[target].(*relationDescriptor)
		if !ok || len(descriptor.generations) == 0 {
			continue
		}
		filtered := *descriptor
		filtered.generations = nil
		for attribute, definition := range descriptor.generations {
			if !generationHasTableOID(definition.planned) {
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
		schema := next.descriptors[descriptor.namespace].Name()
		if eval.statementScope != nil {
			stage := eval.statementScope.op.session.Engine()
			stage.mu.Lock()
			err := stage.captureCheckCommentSnapshotLocked(eval.statementScope, descriptor)
			if err == nil {
				err = stage.captureCheckVisibilitySnapshotLocked(eval.statementScope, descriptor)
			}
			stage.mu.Unlock()
			if err != nil {
				return err
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
			for slot, column := range descriptor.columns {
				if !column.Nullable && row[slot].IsNull() {
					return notNullError(schema, descriptor.name, column.Name, row, eval.Output)
				}
			}
			constraint, err := validateCheckRow(descriptor, row, &eval)
			if err != nil {
				return err
			}
			if constraint != nil {
				return checkDetailError(schema, descriptor.name, constraint.Name, "")
			}
			rows.versions[i].values = row
		}
	}
	return ctx.Err()
}

func validateGenerationProviders(view CatalogRead, d *relationDescriptor) error {
	for _, definition := range d.generations {
		for _, ref := range definition.dependencies {
			address := ref.Address
			address.SubID = 0
			if ref.owner != view.owner.owner || ref.Epoch != view.Epoch() {
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
			if !ok || provider.Kind() != TypeObject && (!relationProvider || ref.Address.SubID != 0 || relation.address.ClassID != RelationClassID) {
				return fmt.Errorf("stuntgres: invalid generation provider")
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
	typ, exists := view.LookupType(usage.Type.ID())
	if !exists || typ != usage.Type || !usage.Valid() {
		return fmt.Errorf("stuntgres: invalid generation plan type")
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
	case columnExpression:
		if n.origin.owner != view.owner.owner || n.origin.Epoch != view.Epoch() || n.origin.Address.ClassID != d.address.ClassID || n.origin.Address.ObjectID != d.address.ObjectID || n.scope != 1 || n.frame != 1 || n.slot < 0 || n.slot >= len(d.columns) || d.attributes[n.slot] != n.origin.Address.SubID || usage != d.columns[n.slot].Usage {
			return fmt.Errorf("stuntgres: invalid generation plan row")
		}
		return nil
	case tableOIDExpression:
		if n.owner.owner != view.owner.owner || n.owner.Epoch != view.Epoch() || n.owner.Address != d.address || usage.Type.ID() != sqltype.Oid {
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
	case arithmeticExpression:
		return pair(n.left, n.right)
	case bitwiseExpression:
		return pair(n.left, n.right)
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
	case likeEscapeExpression:
		return pair(n.pattern, n.escape)
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
	case binaryExpression:
		return args(n.args)
	case datetimeJustifyExpression:
		return args(n.children())
	case datetimeExtractExpression:
		return args(n.children())
	case datetimeAgeFiniteExpression:
		return args(n.children())
	case datetimeMakeExpression:
		return args(n.children())
	case datetimeFormattingExpression:
		return args(n.args[:])
	case stringExpression:
		return args(n.args)
	case mathExpression:
		return args(n.args)
	case nullCountExpression:
		return args(n.args)
	case formatExpression:
		return args(n.args)
	default:
		return fmt.Errorf("stuntgres: unsupported generation plan provider %T", node)
	}
}

func generationDepends(d *relationDescriptor, target ObjectAddress) bool {
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
