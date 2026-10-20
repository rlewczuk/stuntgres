package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type ruleProducer struct {
	root     plan.Node
	old, new []expr.Expr
	sources  []exec.Leaf
}

type ruleCommandSource uint8

const (
	ruleOriginal ruleCommandSource = iota
	ruleAlso
	ruleConditionalInstead
	ruleInstead
)

type ruleCommand struct {
	syntax  parser.Statement
	binding binding
	auth    AuthorizationView
	source  ruleCommandSource
}

type ruleProgram struct {
	commands  []ruleCommand
	events    []catalog.ExpressionDependency
	count     int
	returning int
}

type ruleAncestry struct {
	ref   ids.ObjectRef
	event string
}

func ruleColumnInfo(column catalog.Column) expr.Info {
	return expr.Info{Usage: column.Usage, Pos: parser.Position{Offset: -1}, Property: expr.Properties{RowDependent: true}}
}

func makeRuleProducer(catalog catalog.Read, b binding, event string, outputUses ...[]int) (ruleProducer, error) {
	d := b.Relation
	p := ruleProducer{old: make([]expr.Expr, len(d.Columns)), new: make([]expr.Expr, len(d.Columns)), sources: b.QuerySources}
	for i, column := range d.Columns {
		ref := b.Ref
		ref.Address.SubID = d.Attributes[i]
		p.old[i] = plan.ColumnExpr{Info: ruleColumnInfo(column), Origin: ref, Scope: 1, Frame: 1, Slot: i}
		p.new[i] = p.old[i]
	}
	if event == "INSERT" {
		for i, column := range d.Columns {
			p.old[i] = plan.LiteralExpr{Info: ruleColumnInfo(column), Value: sqltype.Null(column.Usage.Type)}
			p.new[i] = p.old[i]
			if len(b.Insert.Defaults) > i && b.Insert.Defaults[i] != nil {
				p.new[i] = b.Insert.Defaults[i]
			}
		}
		assign := func(output int, value expr.Expr) {
			slot := b.Targets[output]
			if len(b.ArrayTargets) > output && b.ArrayTargets[output] != nil {
				value = plan.AssignmentValueExpr{Info: expr.Info{Usage: d.Columns[slot].Usage, Pos: value.SourcePosition(), Property: expr.MergeProperties(p.new[slot].Properties(), value.Properties())}, Base: p.new[slot], Value: value, Target: b.ArrayTargets[output]}
			}
			p.new[slot] = value
		}
		if b.Query != nil {
			project := b.Query.Root.(*plan.ProjectNode)
			if b.Insert.SubquerySource {
				if len(outputUses) != 0 {
					var err error
					project, err = storedProjectionDemand(project, outputUses[0])
					if err != nil {
						return p, err
					}
				}
				p.root = &plan.DerivedNode{QuerySchema: plan.QuerySchema(project.Schema()), Query: &plan.Query{Root: project}}
				for i := range b.Targets {
					assign(i, plan.ColumnExpr{Info: ruleColumnInfo(project.Schema()[i]), Scope: 1, Frame: project.Frame, Slot: i})
				}
			} else {
				p.root = project.Child
				for i := range b.Targets {
					assign(i, project.Expressions[i])
				}
			}
		} else if len(b.Expressions) == 1 {
			// Released transformInsertStmt gives a single VALUES row target
			// recipes directly, without a VALUES RTE or supplied-datum sharing.
			p.root = &plan.SingletonNode{}
			for i := range b.Targets {
				assign(i, b.Expressions[0][i])
			}
		} else {
			schema := make(plan.QuerySchema, len(b.Targets))
			for i, slot := range b.Targets {
				schema[i] = d.Columns[slot]
				if len(b.Expressions) != 0 {
					schema[i].Usage = b.Expressions[0][i].ResultUsage()
				}
				assign(i, plan.ColumnExpr{Info: ruleColumnInfo(schema[i]), Scope: 1, Frame: 1, Slot: i})
			}
			if len(b.Expressions) == 0 {
				p.root = &plan.SingletonNode{}
			} else {
				p.root = &plan.ValuesNode{QuerySchema: schema, Rows: b.Expressions}
			}
		}
	} else {
		p.sources = append([]exec.Leaf{{Ref: b.Ref, Relation: d, Storage: b.Storage, Uses: b.UsedColumns}}, b.QuerySources...)
		p.root = &plan.SourceNode{QuerySchema: plan.QuerySchema(d.Columns), Ref: b.Ref, Occurrence: 0}
		var from plan.Node
		var condition expr.Expr
		if event == "UPDATE" {
			from = b.Update.From
			condition = b.Update.Predicate
			for i, slot := range b.Targets {
				value := b.Update.Assignments[i]
				if len(b.Update.ArrayTargets) > i && b.Update.ArrayTargets[i] != nil {
					value = plan.AssignmentValueExpr{Info: expr.Info{Usage: d.Columns[slot].Usage, Pos: value.SourcePosition(), Property: expr.MergeProperties(p.new[slot].Properties(), value.Properties())}, Base: p.new[slot], Value: value, Target: b.Update.ArrayTargets[i]}
				}
				p.new[slot] = value
			}
		} else {
			from = b.Delete.Using
			condition = b.Predicate
		}
		mapper := func(bound expr.Expr) (expr.Expr, bool, error) {
			return rewriteViewExpression(catalog, bound, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
				column, ok := expr.(plan.ColumnExpr)
				if !ok {
					return expr, false, nil
				}
				changed := false
				if column.Scope == 0 && column.Frame == 0 {
					column.Scope, column.Frame = 1, 1
					changed = true
				}
				if column.QueryOccurrence != 0 {
					column.QueryOccurrence++
					changed = true
				}
				return column, changed, nil
			})
		}
		for i, expr := range p.new {
			var err error
			p.new[i], _, err = mapper(expr)
			if err != nil {
				return p, err
			}
		}
		if from != nil {
			var err error
			from, err = plan.RewriteQueryExpressions(from, mapper, func(node plan.Node) (plan.Node, error) {
				if source, ok := node.(*plan.SourceNode); ok {
					copy := *source
					copy.Occurrence++
					return &copy, nil
				}
				return node, nil
			})
			if err != nil {
				return p, err
			}
			p.root = ruleJoin(p.root, from)
		}
		if condition != nil {
			var err error
			condition, _, err = mapper(condition)
			if err != nil {
				return p, err
			}
			p.root = &plan.FilterNode{QuerySchema: plan.QuerySchema(p.root.Schema()), Child: p.root, Predicate: condition}
		}
	}
	return p, nil
}

// A NEW substitution may move a stored DEFAULT into the action's query tree.
// Loan only original callable dependencies, after its actual containing recipe
// certifies lifetime (including the existing retained-TEMP epoch bridge).
func loanRuleDefaultDependencies(view catalog.Read, parent *catalog.Relation, slot int, dependencies *[]catalog.ExpressionDependency, private *bool) error {
	definition, ok := parent.ColumnMeta.Defaults[parent.Attributes[slot]]
	if !ok || definition.OID == 0 || definition.Expression == nil || definition.Expression.ResultUsage() != parent.Columns[slot].Usage {
		return fmt.Errorf("stuntgres: invalid rule DEFAULT source")
	}
	if err := validateColumnDefaultProviders(view, parent, definition); err != nil {
		return err
	}
	var failure error
	record := func(captured *catalog.Procedure) {
		if failure != nil || captured == nil {
			return
		}
		current, err := routine.ValidateStoredRoutineDescriptor(view, captured)
		if err != nil {
			failure = err
			return
		}
		if current.Revision() != captured.Revision() {
			failure = catalog.Conflict{}
			return
		}
		for _, original := range definition.Dependencies {
			if original.Address != captured.Address() {
				continue
			}
			if !storedReferenceEpochValid(view, parent, original) {
				failure = ids.ErrCatalogReplaced
				return
			}
			ref := original
			ref.Epoch = view.Epoch() // Project only the certified source recipe.
			for _, existing := range *dependencies {
				if existing.Ref == ref && existing.Revision == captured.Revision() {
					return
				}
			}
			if !*private {
				*dependencies = slices.Clone(*dependencies)
				*private = true
			}
			*dependencies = append(*dependencies, catalog.ExpressionDependency{Ref: ref, Revision: captured.Revision()})
			return
		}
		failure = fmt.Errorf("stuntgres: missing rule DEFAULT routine manifest")
	}
	plan.WalkExpression(definition.Expression, func(node expr.Expr) {
		if failure != nil {
			return
		}
		if expression, ok := node.(plan.CaseExpr); ok {
			for _, arm := range expression.Arms {
				record(arm.Routine)
			}
		}
		captured, err := routine.ExpressionRoutineDescriptor(node)
		if err != nil {
			failure = err
			return
		}
		record(captured)
		if captured != nil {
			failure = routine.WalkRoutineBody(captured, node, func(body *catalog.Procedure) error {
				current, ok := view.Procedure(body.Address())
				if !ok || !routine.SameRoutineDeclaration(current, body) {
					return fmt.Errorf("stuntgres: stale rule DEFAULT SQL body declaration")
				}
				// record still requires the original validated source DEFAULT ref.
				record(current)
				return failure
			})
		}
	})
	return failure
}

func ruleJoin(left, right plan.Node) plan.Node {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	schema := append(slices.Clone(left.Schema()), right.Schema()...)
	return &plan.JoinNode{QuerySchema: plan.QuerySchema(schema), Kind: parser.JoinInner, Left: left, Right: right, LeftWidth: len(left.Schema()), RightWidth: len(right.Schema()), Frame: 1}
}

// The FROM input is grafted below WHERE/aggregate/window evaluation, so original
// producer multiplicity belongs to the query, not to one physical-row callback.
func ruleQueryInput(root plan.Node) plan.Node {
	for {
		switch n := root.(type) {
		case *plan.ProjectNode:
			root = n.Child
		case *plan.FilterNode:
			root = n.Child
		case *plan.SortNode:
			root = n.Child
		case *plan.LimitNode:
			root = n.Child
		case *plan.AggregateNode:
			root = n.Child
		case *plan.WindowNode:
			root = n.Child
		case *plan.EvaluationNode:
			root = n.Child
		case *plan.SetProjectNode:
			root = n.Child
		case *plan.RowLockNode:
			root = n.Child
		default:
			return root
		}
	}
}

func normalizeRuleInsert(b binding) binding {
	if b.Query != nil {
		return b
	}
	schema := make(plan.QuerySchema, len(b.Targets))
	for i, slot := range b.Targets {
		schema[i] = b.Relation.Columns[slot]
	}
	if len(b.Expressions) == 1 {
		b.Query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: schema, Child: &plan.SingletonNode{}, Expressions: b.Expressions[0], Frame: 1}}
	} else {
		projection := make([]expr.Expr, len(schema))
		for i, column := range schema {
			projection[i] = plan.ColumnExpr{Info: ruleColumnInfo(column), Scope: 1, Frame: 1, Slot: i}
		}
		b.Query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: schema, Child: &plan.ValuesNode{QuerySchema: schema, Rows: b.Expressions}, Expressions: projection, Frame: 1}}
	}
	b.CommandQuery = true
	b.Expressions = nil
	return b
}

func ruleSetAction(b binding, syntax parser.Statement) bool {
	if _, selecting := syntax.(*parser.Select); !selecting || b.Query == nil {
		return false
	}
	_, set := ruleQueryInput(b.Query.Root).(*plan.SetNode)
	return set
}

func composeRuleAction(auth AuthorizationView, original binding, event string, r ruleDefinition, action ruleAction) (binding, error) {
	qualified := original.Predicate != nil || original.Update != nil && original.Update.Predicate != nil
	if (qualified || r.condition != nil) && ruleSetAction(action.binding, action.syntax) {
		return binding{}, sqlError("0A000", "conditional UNION/INTERSECT/EXCEPT statements are not implemented")
	}
	catalog := auth.Catalog
	p, err := makeRuleProducer(catalog, original, event, ruleProducerOutputUses(original, r, action.binding))
	if err != nil {
		return binding{}, err
	}
	// Unqualified UPDATE/DELETE actions that do not reference the event
	// namespace run independently of the event table's physical row count.
	// INSERT retains its producer cardinality even without NEW demand.
	if event != "INSERT" && !qualified && r.condition == nil && !ruleBindingHasPseudo(action.binding) {
		p.root, p.sources = &plan.SingletonNode{}, nil
	}
	// Resolve authority at the retained producer boundary, before switching
	// to the event owner's context for stored action refresh and composition.
	caller := auth.State.CurrentUser
	auth.State.CurrentUser = original.Relation.Owner
	b := action.binding
	dependenciesPrivate := false
	b.Insert.Conflict = b.Insert.Conflict.Clone()
	b, err = refreshRuleActionCTEs(auth, b)
	if err != nil {
		return b, err
	}
	if b.Relation != nil {
		current, ok := catalog.Relation(b.Ref.Address)
		if !ok {
			return b, ids.ErrCatalogReplaced
		}
		b, err = refreshRuleBinding(catalog, b, current)
		if err != nil {
			return b, err
		}
	}
	b, err = refreshRuleSources(auth, b)
	if err != nil {
		return b, err
	}
	if _, ok := action.syntax.(*parser.Insert); ok {
		b = normalizeRuleInsert(b)
	}
	b, err = isolateRuleCTEFrames(catalog, original, b, action.syntax)
	if err != nil {
		return b, err
	}
	if len(original.CTEs) != 0 {
		if len(b.CTEs) == 0 {
			b.CTEs = original.CTEs
		} else {
			b.CTEs = slices.Concat(original.CTEs, b.CTEs)
		}
		b.CTESelectBodies = b.CTESelectBodies || original.CTESelectBodies
		b.Rule.CTEs = original.Rule.CTEs
	}
	actionFrame := uint32(1)
	if b.Query != nil {
		actionFrame = b.Query.Root.(*plan.ProjectNode).Frame
	}
	producerFrame := uint32(1)
	if original.Query != nil {
		producerFrame = original.Query.Root.(*plan.ProjectNode).Frame
	}
	actionMax, producerMax := ruleBindingMaxFrame(b), ruleBindingMaxFrame(original)
	if actionFrame != producerFrame || producerMax > 1 || original.Insert.SubquerySource {
		frame := func(old uint32) uint32 {
			if old == 0 {
				return 0
			}
			if old == producerFrame {
				return actionFrame
			}
			return old + actionMax + 1
		}
		rootFrame := frame
		if original.Insert.SubquerySource {
			// The derived SELECT owns a private lexical frame. Only its output
			// Vars enter the action frame; inner columns must not be graft-shifted.
			rootFrame = func(old uint32) uint32 {
				if old == 0 {
					return 0
				}
				return old + actionMax + 1
			}
		}
		p.root, _, err = rewriteRuleFrames(catalog, p.root, nil, rootFrame, original.CTEs)
		if err != nil {
			return b, err
		}
		for i, expr := range p.old {
			_, p.old[i], err = rewriteRuleFrames(catalog, nil, expr, frame)
			if err != nil {
				return b, err
			}
		}
		for i, expr := range p.new {
			_, p.new[i], err = rewriteRuleFrames(catalog, nil, expr, frame)
			if err != nil {
				return b, err
			}
		}
	}
	width := len(p.root.Schema())
	targetWidth := 0
	if _, ok := action.syntax.(*parser.Update); ok {
		targetWidth = len(b.Relation.Columns)
	}
	if _, ok := action.syntax.(*parser.Delete); ok {
		targetWidth = len(b.Relation.Columns)
	}
	sourceOffset := len(p.sources)
	b, err = offsetRuleSources(catalog, b, sourceOffset, original.CTEs)
	if err != nil {
		return b, err
	}
	windowSlots := make(map[int]int)
	targetOffset, shift := targetWidth, true
	substitute := func(bound expr.Expr) (expr.Expr, bool, error) {
		if window, ok := bound.(*plan.WindowExpr); ok && window.Frame == actionFrame {
			if slot, ok := windowSlots[window.Slot]; ok && slot != window.Slot {
				copy := *window
				copy.Slot = slot
				return &copy, true, nil
			}
		}
		column, ok := bound.(plan.ColumnExpr)
		if !ok {
			return bound, false, nil
		}
		if column.Scope == analyze.RulePseudoScope {
			recipes := p.old
			if column.Frame == 2 {
				recipes = p.new
			}
			slot := slices.Index(original.Relation.Attributes, column.Origin.Address.SubID)
			if slot < 0 || slot >= len(recipes) {
				return nil, false, ids.ErrCatalogReplaced
			}
			if column.Frame == 2 && len(original.Insert.Defaults) > slot && original.Insert.Defaults[slot] != nil {
				if err := loanRuleDefaultDependencies(catalog, original.Relation, slot, &b.Dependencies, &dependenciesPrivate); err != nil {
					return nil, false, err
				}
			}
			recipe := recipes[slot]
			if column.Frame == 2 {
				if err := validateRuleNewUpdateOutput(recipe); err != nil {
					return nil, false, err
				}
			}
			if targetOffset != 0 {
				var err error
				recipe, _, err = rewriteViewExpression(catalog, recipe, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
					c, ok := expr.(plan.ColumnExpr)
					if ok && c.Scope == 1 && c.Frame == actionFrame {
						c.Slot += targetOffset
						if c.Virtual != nil {
							c.RowStart += targetOffset
						}
						return c, true, nil
					}
					return expr, false, nil
				})
				if err != nil {
					return nil, false, err
				}
			}
			return recipe, true, nil
		}
		changed := false
		if shift && column.Scope == 1 && column.Frame == actionFrame && column.Slot >= targetWidth {
			column.Slot += width
			if column.Virtual != nil {
				column.RowStart += width
			}
			changed = true
		}
		return column, changed, nil
	}
	var opaque map[plan.Node]bool
	finish := ruleQueryFinalizer(catalog)
	tree := newRuleTreeMapper(catalog, nil, 0, original.CTEs, ruleTreeMapping{
		leaf:   substitute,
		finish: finish,
		boundary: func(node plan.Node) (plan.Node, bool) {
			if !opaque[node] {
				return node, false
			}
			if filter, ok := node.(*plan.FilterNode); ok {
				copy := *filter
				return &copy, true
			}
			copy := *node.(*plan.JoinNode)
			return &copy, true
		},
	})
	mapper := func(expr expr.Expr) (expr.Expr, bool, error) {
		previousOffset, previousShift := targetOffset, shift
		targetOffset, shift = targetWidth, true
		defer func() { targetOffset, shift = previousOffset, previousShift }()
		return tree.expression(expr)
	}
	mapQuery := func(root plan.Node, input bool) (plan.Node, error) {
		previousOffset, previousShift := targetOffset, shift
		targetOffset, shift = targetWidth, true
		if input {
			targetOffset, shift = 0, false
		}
		defer func() { targetOffset, shift = previousOffset, previousShift }()
		return tree.query(root)
	}
	mapTargets := func(targets []*plan.ArrayAssignmentTarget) ([]*plan.ArrayAssignmentTarget, error) {
		return mapRuleAssignmentTargets(targets, mapper, func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil })
	}
	b.ArrayTargets, err = mapTargets(b.ArrayTargets)
	if err != nil {
		return b, err
	}
	condition := r.condition
	var conditionSources []exec.Leaf
	if r.conditionBinding != nil {
		cb, refreshErr := refreshRuleSources(auth, *r.conditionBinding)
		if refreshErr != nil {
			return b, refreshErr
		}
		cb, err = offsetRuleSources(catalog, cb, sourceOffset+len(b.QuerySources))
		if err != nil {
			return b, err
		}
		condition = cb.Query.Root.(*plan.ProjectNode).Expressions[0]
		_, condition, err = rewriteRuleFrames(catalog, nil, condition, func(old uint32) uint32 {
			if old == 0 {
				return 0
			}
			if old == 1 {
				return actionFrame
			}
			return old + actionMax + producerMax + 3
		})
		if err != nil {
			return b, err
		}
		conditionSources = cb.QuerySources
		b.Dependencies = append(slices.Clone(b.Dependencies), cb.Dependencies...)
		b.Prepared.SequenceConstants = append(slices.Clone(b.Prepared.SequenceConstants), cb.Prepared.SequenceConstants...)
		b.Prepared.RegtypeConstants = append(slices.Clone(b.Prepared.RegtypeConstants), cb.Prepared.RegtypeConstants...)
	}
	if condition != nil {
		condition, _, err = mapper(condition)
		if err != nil {
			return b, err
		}
	}
	graft := func(root plan.Node) (plan.Node, error) {
		if root == nil {
			return p.root, nil
		}
		input := ruleQueryInput(root)
		correlated := ruleQueryHasPseudo(input)
		right := input
		if correlated {
			innerFrame := actionMax + producerMax + 3
			right, _, err = rewriteRuleFrames(catalog, right, nil, func(old uint32) uint32 {
				if old == actionFrame {
					return innerFrame
				}
				return old
			}, b.CTEs)
			if err != nil {
				return nil, err
			}
			right, err = mapQuery(right, true)
			if err != nil {
				return nil, err
			}
			projection := make([]expr.Expr, len(right.Schema()))
			for i, column := range right.Schema() {
				projection[i] = plan.ColumnExpr{Info: ruleColumnInfo(column), Scope: 1, Frame: innerFrame, Slot: i}
			}
			right = &plan.ProjectNode{QuerySchema: plan.QuerySchema(right.Schema()), Child: right, Expressions: projection, Frame: innerFrame, RuleInvocation: true}
		} else {
			right, err = mapQuery(right, true)
			if err != nil {
				return nil, err
			}
		}
		joined := ruleJoin(p.root, right).(*plan.JoinNode)
		joined.Frame = actionFrame
		joined.CorrelatedRight = correlated
		var replacement plan.Node = joined
		if condition != nil && targetWidth == 0 {
			replacement = &plan.FilterNode{QuerySchema: plan.QuerySchema(joined.Schema()), Child: joined, Predicate: condition}
		}
		patched, err := ruleGraftSchemas(root, input, replacement, windowSlots)
		if err != nil {
			return nil, err
		}
		if opaque == nil {
			opaque = make(map[plan.Node]bool)
		}
		opaque[replacement] = true
		substituted, err := mapQuery(patched, false)
		if err != nil {
			return nil, err
		}
		if substituted == patched {
			return finish(substituted)
		}
		return substituted, nil
	}
	if b.Query != nil {
		copy := *b.Query
		copy.Root, err = graft(copy.Root)
		if err != nil {
			return b, err
		}
		b.Query = &copy
	}
	if b.Update != nil {
		u := *b.Update
		u.Assignments = slices.Clone(u.Assignments)
		u.ArrayTargets, err = mapTargets(u.ArrayTargets)
		if err != nil {
			return b, err
		}
		for i, expr := range u.Assignments {
			u.Assignments[i], _, err = mapper(expr)
			if err != nil {
				return b, err
			}
		}
		u.Predicate, _, err = mapper(u.Predicate)
		if err != nil {
			return b, err
		}
		if condition != nil {
			u.Predicate = viewWritePredicate([]expr.Expr{condition}, u.Predicate)
		}
		u.From, err = graft(u.From)
		if err != nil {
			return b, err
		}
		u.SourceWidth = len(u.From.Schema())
		b.Update = &u
	}
	if _, ok := action.syntax.(*parser.Delete); ok {
		b.Predicate, _, err = mapper(b.Predicate)
		if err != nil {
			return b, err
		}
		if condition != nil {
			b.Predicate = viewWritePredicate([]expr.Expr{condition}, b.Predicate)
		}
		b.Delete.Using, err = graft(b.Delete.Using)
		if err != nil {
			return b, err
		}
		b.Delete.SourceWidth = len(b.Delete.Using.Schema())
	}
	var definitions []*plan.CTEDefinition
	for i, definition := range b.CTEs {
		mapped, mapErr := tree.definition(definition)
		if mapErr != nil {
			return b, mapErr
		}
		if mapped != definition {
			if definitions == nil {
				definitions = slices.Clone(b.CTEs)
			}
			definitions[i] = mapped
		}
	}
	if definitions != nil {
		b.CTEs = definitions
	}
	b.QuerySources = append(slices.Clone(p.sources), b.QuerySources...)
	b.QuerySources = append(b.QuerySources, conditionSources...)
	for i := range b.QuerySources {
		if i < sourceOffset {
			if b.QuerySources[i].Principal == (authorization.PrincipalRef{}) {
				b.QuerySources[i].Principal = caller
			}
		} else {
			b.QuerySources[i].Principal = auth.State.CurrentUser
		}
	}
	b.SubqueryDemand = b.SubqueryDemand || original.SubqueryDemand
	b.Dependencies = append(slices.Clone(original.Dependencies), b.Dependencies...)
	b.Parameters = original.Parameters
	return b, nil
}

func (e *Engine) expandRulesLocked(scope *StatementScope, auth AuthorizationView, stmt parser.Statement, b binding, ancestry []ruleAncestry, program *ruleProgram) (int, error) {
	event := ruleEvent(stmt)
	firstCommand := len(program.commands)
	b = physicalBinding(b)
	if event == "" || b.Relation == nil {
		return e.appendRuleCommand(program, auth, stmt, b), nil
	}
	current, ok := auth.Catalog.Relation(b.Ref.Address)
	if !ok || b.Ref.Epoch != auth.Catalog.Epoch() {
		return -1, ids.ErrCatalogReplaced
	}
	var err error
	b, err = refreshRuleBinding(auth.Catalog, b, current)
	if err != nil {
		return -1, err
	}
	if insert, ok := stmt.(*parser.Insert); ok && b.Rule.Template {
		b.Insert.Defaults = nil
		BindInsertDefaults(e.catalog.owner(), &b, auth.Catalog, insert.Override)
	}
	if current.RelationKind != catalog.ViewRelation {
		b.Storage = e.authorizationStorageLocked(auth, b.Ref.Address)
	}
	rules := rulesOf(b.Relation)
	matched := slices.ContainsFunc(rules, func(r ruleDefinition) bool { return r.event == event })
	if matched {
		dependency := catalog.ExpressionDependency{Ref: b.Ref, Revision: current.Revision()}
		if !slices.Contains(program.events, dependency) {
			program.events = append(program.events, dependency)
		}
	}
	if current.RelationKind == catalog.ViewRelation && !matched {
		b.ViewWrite = nil
		if err := e.bindViewWrite(&b, auth, stmt); err != nil {
			return -1, err
		}
		return e.expandRulesLocked(scope, auth, stmt, b, ancestry, program)
	}
	if len(rules) == 0 {
		return e.appendRuleCommand(program, auth, stmt, b), nil
	}
	key := ruleAncestry{b.Ref, event}
	if slices.Contains(ancestry, key) {
		return -1, sqlError("42P17", fmt.Sprintf(`infinite recursion detected in rules for relation "%s"`, b.Relation.Name()))
	}
	ancestry = append(ancestry, key)
	if s, ok := stmt.(*parser.Insert); ok && s.Conflict != nil {
		for _, r := range rules {
			if r.event == "UPDATE" || r.event == "INSERT" && len(r.actions) != 0 {
				return -1, sqlError("0A000", "INSERT with ON CONFLICT clause cannot be used with table that has INSERT or UPDATE rules")
			}
		}
	}
	original := true
	conditionalInstead := false
	for _, r := range rules {
		if r.event == event && r.instead && r.condition == nil {
			original = false
		}
		if r.event == event && r.instead && r.condition != nil {
			conditionalInstead = true
		}
	}
	residual := b
	if original {
		var err error
		residual, err = ruleResidual(auth, b, event, rules)
		if err != nil {
			return -1, err
		}
	}
	count := -1
	if event == "INSERT" && original {
		var err error
		start := len(program.commands)
		count, err = e.expandRuleResidualLocked(scope, auth, stmt, residual, ancestry, program)
		if err != nil {
			return -1, err
		}
		if conditionalInstead {
			markRuleCommandSource(program, start, ruleConditionalInstead)
		}
	}
	providers := 0
	for _, r := range rules {
		if r.event != event {
			continue
		}
		for _, address := range r.references {
			whole := address
			whole.SubID = 0
			if descriptor, ok := auth.Catalog.Find(whole); ok {
				b.Dependencies = append(b.Dependencies, catalog.ExpressionDependency{Ref: e.catalog.Domain.Ref(auth.Catalog.Epoch(), address), Revision: descriptor.Revision()})
			}
		}
		for _, action := range r.actions {
			actionAuth := auth
			actionAuth.State.CurrentUser = current.Owner
			composed, err := composeRuleAction(auth, b, event, r, action)
			if err != nil {
				return -1, err
			}
			if ruleReturning(stmt) == nil {
				composed.Returning = nil
			} else if action.binding.Returning != nil {
				providers++
				if providers > 1 {
					return -1, sqlError("0A000", "cannot have RETURNING lists in multiple rules")
				}
				composed.Returning, err = ruleReturningProjection(auth.Catalog, b.Returning, action.binding.Returning)
				if err != nil {
					return -1, err
				}
			}
			start := len(program.commands)
			index, err := e.expandRulesLocked(scope, actionAuth, action.syntax, composed, ancestry, program)
			if err != nil {
				return -1, err
			}
			source := ruleInstead
			if !r.instead {
				source = ruleAlso
			} else if r.condition != nil {
				source = ruleConditionalInstead
			}
			markRuleCommandSource(program, start, source)
			if composed.Returning != nil && ruleReturning(stmt) != nil {
				program.returning = index
			}
			if !original && r.instead && ruleEvent(action.syntax) == event {
				count = index
			}
		}
	}
	if event != "INSERT" && original {
		var err error
		start := len(program.commands)
		count, err = e.expandRuleResidualLocked(scope, auth, stmt, residual, ancestry, program)
		if err != nil {
			return -1, err
		}
		if conditionalInstead {
			markRuleCommandSource(program, start, ruleConditionalInstead)
		}
	}
	if (!original || conditionalInstead) && ruleReturning(stmt) != nil && providers == 0 {
		return -1, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`cannot perform %s RETURNING on relation "%s"`, event, b.Relation.Name()), Hint: "You need an unconditional ON " + event + " DO INSTEAD rule with a RETURNING clause."}
	}
	if len(parser.StatementWith(stmt)) != 0 && len(program.commands)-firstCommand > 1 {
		return -1, sqlError("0A000", "WITH cannot be used in a query that is rewritten by rules into multiple queries")
	}
	return count, nil
}

func (e *Engine) appendRuleCommand(program *ruleProgram, auth AuthorizationView, stmt parser.Statement, b binding) int {
	index := len(program.commands)
	program.commands = append(program.commands, ruleCommand{syntax: stmt, binding: b, auth: auth})
	return index
}

func markRuleCommandSource(program *ruleProgram, start int, source ruleCommandSource) {
	for i := start; i < len(program.commands); i++ {
		if program.commands[i].source == ruleOriginal {
			program.commands[i].source = source
		}
	}
}

func ruleResidual(auth AuthorizationView, b binding, event string, rules []ruleDefinition) (binding, error) {
	catalog := auth.Catalog
	p, err := makeRuleProducer(catalog, b, event)
	if err != nil {
		return b, err
	}
	var predicates []expr.Expr
	for _, r := range rules {
		if r.event != event || !r.instead || r.condition == nil {
			continue
		}
		qualification := r.condition
		if r.conditionBinding != nil {
			cb, refreshErr := refreshRuleSources(auth, *r.conditionBinding)
			if refreshErr != nil {
				return b, refreshErr
			}
			cb, err = offsetRuleSources(catalog, cb, len(b.QuerySources))
			if err != nil {
				return b, err
			}
			qualification = cb.Query.Root.(*plan.ProjectNode).Expressions[0]
			maxFrame := ruleBindingMaxFrame(b)
			_, qualification, err = rewriteRuleFrames(catalog, nil, qualification, func(old uint32) uint32 {
				if old <= 1 {
					return old
				}
				return old + maxFrame + 1
			})
			if err != nil {
				return b, err
			}
			b.QuerySources = append(slices.Clone(b.QuerySources), cb.QuerySources...)
			b.Dependencies = append(slices.Clone(b.Dependencies), cb.Dependencies...)
			b.Prepared.SequenceConstants = append(slices.Clone(b.Prepared.SequenceConstants), cb.Prepared.SequenceConstants...)
			b.Prepared.RegtypeConstants = append(slices.Clone(b.Prepared.RegtypeConstants), cb.Prepared.RegtypeConstants...)
		}
		tree := newRuleTreeMapper(catalog, nil, 0, b.CTEs, ruleTreeMapping{finish: ruleQueryFinalizer(catalog), leaf: func(expr expr.Expr) (expr.Expr, bool, error) {
			if column, ok := expr.(plan.ColumnExpr); ok && column.Scope == analyze.RulePseudoScope {
				recipes := p.old
				if column.Frame == 2 {
					recipes = p.new
				}
				slot := slices.Index(b.Relation.Attributes, column.Origin.Address.SubID)
				if slot < 0 || slot >= len(recipes) {
					return nil, false, ids.ErrCatalogReplaced
				}
				recipe := recipes[slot]
				if column.Frame == 2 {
					if err := validateRuleNewUpdateOutput(recipe); err != nil {
						return nil, false, err
					}
				}
				return recipe, true, nil
			}
			return expr, false, nil
		}})
		condition, _, err := tree.expression(qualification)
		if err != nil {
			return b, err
		}
		predicates = append(predicates, plan.TruthTestExpr{Info: expr.Info{Usage: condition.ResultUsage(), Pos: condition.SourcePosition(), Property: condition.Properties()}, Child: condition, Truth: plan.True, Not: true})
	}
	if len(predicates) == 0 {
		return b, nil
	}
	if event == "INSERT" {
		schema := make(plan.QuerySchema, len(b.Targets))
		frame := uint32(1)
		if b.Query != nil {
			frame = b.Query.Root.(*plan.ProjectNode).Frame
		}
		projection := make([]expr.Expr, len(b.Targets))
		for i, slot := range b.Targets {
			schema[i] = b.Relation.Columns[slot]
			projection[i] = p.new[slot]
		}
		root := &plan.FilterNode{QuerySchema: plan.QuerySchema(p.root.Schema()), Child: p.root, Predicate: viewWritePredicate(predicates, nil)}
		b.Query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: schema, Child: root, Expressions: projection, Frame: frame}}
		b.CommandQuery = true
		b.Expressions = nil
	} else if event == "UPDATE" {
		u := *b.Update
		u.Predicate = viewWritePredicate(predicates, u.Predicate)
		b.Update = &u
	} else {
		b.Predicate = viewWritePredicate(predicates, b.Predicate)
	}
	return b, nil
}

func ruleReturningProjection(catalog catalog.Read, original, provider *boundReturning) (*boundReturning, error) {
	copy := *original
	// Output names/usages belong to the caller; column privileges belong to
	// the physical provider whose expressions replace those output Vars.
	copy.Reads = provider.Reads
	copy.ReadRequired = provider.ReadRequired
	copy.VisibilitySnapshot = copy.VisibilitySnapshot || provider.VisibilitySnapshot
	copy.Expressions = slices.Clone(original.Expressions)
	for i, bound := range copy.Expressions {
		var err error
		copy.Expressions[i], _, err = rewriteViewExpression(catalog, bound, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
			column, ok := expr.(plan.ReturningColumnExpr)
			if !ok {
				return expr, false, nil
			}
			if column.Slot >= len(provider.Expressions) {
				return nil, false, ids.ErrCatalogReplaced
			}
			return provider.Expressions[column.Slot], true, nil
		})
		if err != nil {
			return nil, err
		}
	}
	return &copy, nil
}

func (e *Engine) expandRuleResidualLocked(scope *StatementScope, auth AuthorizationView, stmt parser.Statement, b binding, ancestry []ruleAncestry, program *ruleProgram) (int, error) {
	if b.Relation.RelationKind != catalog.ViewRelation {
		return e.appendRuleCommand(program, auth, stmt, b), nil
	}
	prior := viewWriteOf(b.ViewWrite)
	b.ViewWrite = nil
	if err := e.bindViewWrite(&b, auth, stmt, b.Ref); err != nil {
		return -1, err
	}
	if b.ViewWrite != nil && prior != nil {
		viewWriteOf(b.ViewWrite).layers = append(slices.Clone(prior.layers), viewWriteOf(b.ViewWrite).layers...)
		viewWriteOf(b.ViewWrite).logical = prior.logical
	}
	return e.expandRulesLocked(scope, auth, stmt, b, ancestry, program)
}

func refreshRuleBinding(catalog catalog.Read, b binding, current *catalog.Relation) (binding, error) {
	old := b.Relation
	if slices.Equal(old.Attributes, current.Attributes) {
		b.Relation = current
		return b, nil
	}
	slots := func(original []int) ([]int, error) {
		next := slices.Clone(original)
		for i, slot := range original {
			if slot < 0 || slot >= len(old.Attributes) {
				return nil, ids.ErrCatalogReplaced
			}
			next[i] = slices.Index(current.Attributes, old.Attributes[slot])
			if next[i] < 0 {
				return nil, ids.ErrCatalogReplaced
			}
		}
		return next, nil
	}
	var err error
	b.Targets, err = slots(b.Targets)
	if err != nil {
		return b, err
	}
	b.UsedColumns, err = slots(b.UsedColumns)
	if err != nil {
		return b, err
	}
	if b.Insert.Conflict != nil {
		conflict, err := refreshRuleConflict(catalog, b, current, slots)
		if err != nil {
			return b, err
		}
		b.Insert.Conflict = conflict
	}
	leaf := func(expr expr.Expr) (expr.Expr, bool, error) {
		if column, ok := expr.(plan.ColumnExpr); ok && column.Scope == 1 && column.Frame == 1 && column.Slot >= len(old.Columns) {
			column.Slot += len(current.Columns) - len(old.Columns)
			if column.Virtual != nil {
				column.RowStart += len(current.Columns) - len(old.Columns)
			}
			return column, true, nil
		}
		column, ok := expr.(plan.ColumnExpr)
		if !ok || column.Origin.Address.ObjectID != b.Ref.Address.ObjectID {
			return expr, false, nil
		}
		if !(column.Scope == 0 && column.Frame == 0) && !(column.Scope == 1 && column.Frame == 1 && column.Slot < len(old.Columns)) {
			return expr, false, nil
		}
		column.Slot = slices.Index(current.Attributes, column.Origin.Address.SubID)
		if column.Slot < 0 {
			return nil, false, ids.ErrCatalogReplaced
		}
		return column, true, nil
	}
	mapExpr := func(expr expr.Expr) (expr.Expr, error) {
		next, _, err := rewriteViewExpression(catalog, expr, nil, leaf)
		return next, err
	}
	mapTargets := func(targets []*plan.ArrayAssignmentTarget) ([]*plan.ArrayAssignmentTarget, error) {
		return mapRuleAssignmentTargets(targets, func(expr expr.Expr) (expr.Expr, bool, error) { return rewriteViewExpression(catalog, expr, nil, leaf) }, func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil })
	}
	b.ArrayTargets, err = mapTargets(b.ArrayTargets)
	if err != nil {
		return b, err
	}
	if b.Query != nil {
		next := *b.Query
		next.Root, err = plan.RewriteQueryExpressions(next.Root, func(expr expr.Expr) (expr.Expr, bool, error) { return rewriteViewExpression(catalog, expr, nil, leaf) })
		if err != nil {
			return b, err
		}
		b.Query = &next
	}
	if b.Returning != nil {
		next := *b.Returning
		next.Expressions = slices.Clone(next.Expressions)
		for i, expr := range next.Expressions {
			next.Expressions[i], err = mapExpr(expr)
			if err != nil {
				return b, err
			}
		}
		b.Returning = &next
	}
	if b.Update != nil {
		u := *b.Update
		u.Assignments = slices.Clone(u.Assignments)
		u.ArrayTargets, err = mapTargets(u.ArrayTargets)
		if err != nil {
			return b, err
		}
		for i, expr := range u.Assignments {
			u.Assignments[i], _, err = rewriteViewExpression(catalog, expr, nil, leaf)
			if err != nil {
				return b, err
			}
		}
		u.Predicate, _, err = rewriteViewExpression(catalog, u.Predicate, nil, leaf)
		if err != nil {
			return b, err
		}
		b.Update = &u
	}
	b.Predicate, _, err = rewriteViewExpression(catalog, b.Predicate, nil, leaf)
	if err != nil {
		return b, err
	}
	b.Relation = current
	return b, nil
}

func refreshRuleSources(auth AuthorizationView, b binding) (binding, error) {
	catalog := auth.Catalog
	var sources []exec.Leaf
	for i, source := range b.QuerySources {
		current, ok := catalog.Relation(source.Ref.Address)
		if !ok || source.Relation == nil || source.Ref.Domain() != auth.Owner.Domain || source.Ref.Epoch != catalog.Epoch() || source.Ref.Address.SubID != 0 {
			return b, ids.ErrCatalogReplaced
		}
		storage := auth.engine.authorizationStorageLocked(auth, source.Ref.Address)
		if current == source.Relation && storage == source.Storage {
			continue
		}
		if sources == nil {
			sources = slices.Clone(b.QuerySources)
		}
		updated := source
		updated.Relation = current
		updated.Storage = storage
		if !slices.Equal(source.Relation.Attributes, current.Attributes) {
			updated.Uses = slices.Clone(source.Uses)
			for j, slot := range source.Uses {
				if slot < 0 || slot >= len(source.Relation.Attributes) {
					return b, ids.ErrCatalogReplaced
				}
				updated.Uses[j] = slices.Index(current.Attributes, source.Relation.Attributes[slot])
				if updated.Uses[j] < 0 {
					return b, ids.ErrCatalogReplaced
				}
			}
		}
		sources[i] = updated
	}
	if sources != nil {
		b.QuerySources = sources
	}
	return b, nil
}

func refreshRuleConflict(catalog catalog.Read, b binding, current *catalog.Relation, slots func([]int) ([]int, error)) (*analyze.InsertConflict, error) {
	c := *b.Insert.Conflict
	var err error
	c.Targets, err = slots(c.Targets)
	if err != nil {
		return nil, err
	}
	c.UpdateTargets, err = slots(c.UpdateTargets)
	if err != nil {
		return nil, err
	}
	c.Reads, err = slots(c.Reads)
	if err != nil {
		return nil, err
	}
	c.ActionReads, err = slots(c.ActionReads)
	if err != nil {
		return nil, err
	}
	oldWidth, width := len(b.Relation.Attributes), len(current.Attributes)
	leaf := func(expr expr.Expr) (expr.Expr, bool, error) {
		column, ok := expr.(plan.ColumnExpr)
		if !ok || column.Origin.Address.ClassID != b.Ref.Address.ClassID || column.Origin.Address.ObjectID != b.Ref.Address.ObjectID {
			return expr, false, nil
		}
		if !(column.Scope == 0 && column.Frame == 0) && !(column.Scope == 1 && column.Frame == 1) {
			return expr, false, nil
		}
		slot := current.IndexForAttribute(column.Origin.Address.SubID)
		if slot < 0 {
			return nil, false, ids.ErrCatalogReplaced
		}
		if column.Scope == 1 && column.Frame == 1 && column.Slot >= oldWidth {
			slot += width
		}
		column.Slot = slot
		if column.Virtual != nil {
			if column.RowStart >= oldWidth {
				column.RowStart = width
			}
			column.RowWidth = width
		}
		return column, true, nil
	}
	mapper := newRuleTreeMapper(catalog, nil, 0, nil, ruleTreeMapping{leaf: leaf})
	mapped, err := mapRuleBindingRecipes(binding{Insert: insertBinding{Conflict: &c}}, mapper)
	if err != nil {
		return nil, err
	}
	c = *mapped.Insert.Conflict
	c.Width = width
	return &c, nil
}

func ruleBindingHasPseudo(b binding) bool {
	found := false
	inspect := func(bound expr.Expr) {
		plan.WalkExpression(bound, func(node expr.Expr) {
			if column, ok := node.(plan.ColumnExpr); ok && column.Scope == analyze.RulePseudoScope {
				found = true
			}
		})
	}
	query := func(root plan.Node) {
		if root != nil && !found {
			plan.VisitCTEQuery(root, func(plan.Node) {}, inspect, true)
		}
	}
	expression := func(bound expr.Expr) {
		if bound != nil && !found {
			query(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
		}
	}
	targets := func(items []*plan.ArrayAssignmentTarget) {
		for _, target := range items {
			for ; target != nil; target = target.Next {
				for _, sub := range target.Node.Subscripts {
					expression(sub.Lower)
					expression(sub.Upper)
				}
			}
		}
	}
	if b.Query != nil {
		query(b.Query.Root)
	}
	for _, row := range b.Expressions {
		for _, expr := range row {
			expression(expr)
		}
	}
	expression(b.Predicate)
	query(b.Delete.Using)
	targets(b.ArrayTargets)
	if b.Update != nil {
		query(b.Update.From)
		expression(b.Update.Predicate)
		for _, expr := range b.Update.Assignments {
			expression(expr)
		}
		targets(b.Update.ArrayTargets)
	}
	if b.Returning != nil {
		for _, expr := range b.Returning.Expressions {
			expression(expr)
		}
	}
	if b.Insert.Conflict != nil {
		expression(b.Insert.Conflict.Predicate)
		for _, key := range b.Insert.Conflict.Keys {
			expression(key.Expression)
		}
		if b.Insert.Conflict.Update != nil {
			expression(b.Insert.Conflict.Update.Predicate)
			for _, expr := range b.Insert.Conflict.Update.Assignments {
				expression(expr)
			}
			targets(b.Insert.Conflict.Update.ArrayTargets)
		}
	}
	return found
}
