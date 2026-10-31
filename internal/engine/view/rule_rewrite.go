package view

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
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type ruleProducer struct {
	Root     plan.Node
	old, New []expr.Expr
	Sources  []exec.Leaf
}

type ruleCommandSource uint8

const (
	RuleOriginal ruleCommandSource = iota
	RuleAlso
	RuleConditionalInstead
	RuleInstead
)

type RuleCommand struct {
	Syntax  parser.Statement
	Binding analyze.Binding
	Auth    RuleAuthority
	Source  ruleCommandSource
}

type RuleProgram struct {
	Commands  []RuleCommand
	Events    []catalog.ExpressionDependency
	Count     int
	Returning int
}

type ruleAncestry struct {
	ref   ids.ObjectRef
	event string
}

func RuleColumnInfo(column catalog.Column) expr.Info {
	return expr.Info{Usage: column.Usage, Pos: parser.Position{Offset: -1}, Property: expr.Properties{RowDependent: true}}
}

func MakeRuleProducer(catalog catalog.Read, b analyze.Binding, event string, outputUses ...[]int) (ruleProducer, error) {
	d := b.Relation
	p := ruleProducer{old: make([]expr.Expr, len(d.Columns)), New: make([]expr.Expr, len(d.Columns)), Sources: b.QuerySources}
	for i, column := range d.Columns {
		ref := b.Ref
		ref.Address.SubID = d.Attributes[i]
		p.old[i] = plan.ColumnExpr{Info: RuleColumnInfo(column), Origin: ref, Scope: 1, Frame: 1, Slot: i}
		p.New[i] = p.old[i]
	}
	if event == "INSERT" {
		for i, column := range d.Columns {
			p.old[i] = plan.LiteralExpr{Info: RuleColumnInfo(column), Value: sqltype.Null(column.Usage.Type)}
			p.New[i] = p.old[i]
			if len(b.Insert.Defaults) > i && b.Insert.Defaults[i] != nil {
				p.New[i] = b.Insert.Defaults[i]
			}
		}
		assign := func(output int, value expr.Expr, supplied bool) {
			slot := b.Targets[output]
			// DEFAULT is an absent supplied expression, not an executable nil
			// or SQL NULL. Keep the initialized default/null recipe. Identity
			// override inputs likewise do not replace their default demand;
			// materialized VALUES outputs have already consumed that default.
			if value == nil || supplied && len(b.Insert.IdentityDiscard) > slot && b.Insert.IdentityDiscard[slot] {
				return
			}
			if len(b.ArrayTargets) > output && b.ArrayTargets[output] != nil {
				value = plan.AssignmentValueExpr{Info: expr.Info{Usage: d.Columns[slot].Usage, Pos: value.SourcePosition(), Property: expr.MergeProperties(p.New[slot].Properties(), value.Properties())}, Base: p.New[slot], Value: value, Target: b.ArrayTargets[output]}
			}
			p.New[slot] = value
		}
		if b.Query != nil {
			project := b.Query.Root.(*plan.ProjectNode)
			if b.Insert.SubquerySource {
				if len(outputUses) != 0 {
					var err error
					project, err = StoredProjectionDemand(project, outputUses[0])
					if err != nil {
						return p, err
					}
				}
				p.Root = &plan.DerivedNode{QuerySchema: plan.QuerySchema(project.Schema()), Query: &plan.Query{Root: project}}
				for i := range b.Targets {
					assign(i, plan.ColumnExpr{Info: RuleColumnInfo(project.Schema()[i]), Scope: 1, Frame: project.Frame, Slot: i}, true)
				}
			} else {
				p.Root = project.Child
				for i := range b.Targets {
					assign(i, project.Expressions[i], true)
				}
			}
		} else if len(b.Expressions) == 1 {
			// Released transformInsertStmt gives a single VALUES row target
			// recipes directly, without a VALUES RTE or supplied-datum sharing.
			p.Root = &plan.SingletonNode{}
			for i := range b.Targets {
				assign(i, b.Expressions[0][i], true)
			}
		} else {
			// VALUES rows execute independently of NEW's output Vars. Replace
			// default sentinels with their real destination recipes before
			// exposing this producer, without changing retained binding rows.
			var rows [][]expr.Expr
			for rowIndex, row := range b.Expressions {
				var values []expr.Expr
				for ordinal, value := range row {
					slot := b.Targets[ordinal]
					if value != nil && (len(b.Insert.IdentityDiscard) <= slot || !b.Insert.IdentityDiscard[slot]) {
						continue
					}
					if values == nil {
						values = slices.Clone(row)
					}
					values[ordinal] = p.New[slot]
				}
				if values != nil {
					if rows == nil {
						rows = slices.Clone(b.Expressions)
					}
					rows[rowIndex] = values
				}
			}
			if rows == nil {
				rows = b.Expressions
			}
			schema := make(plan.QuerySchema, len(b.Targets))
			for i, slot := range b.Targets {
				schema[i] = d.Columns[slot]
				if len(rows) != 0 {
					schema[i].Usage = rows[0][i].ResultUsage()
				}
				assign(i, plan.ColumnExpr{Info: RuleColumnInfo(schema[i]), Scope: 1, Frame: 1, Slot: i}, false)
			}
			if len(b.Expressions) == 0 {
				p.Root = &plan.SingletonNode{}
			} else {
				p.Root = &plan.ValuesNode{QuerySchema: schema, Rows: rows}
			}
		}
	} else {
		p.Sources = append([]exec.Leaf{{Ref: b.Ref, Relation: d, Storage: b.Storage, Uses: b.UsedColumns}}, b.QuerySources...)
		if len(outputUses) != 0 {
			p.Sources[0].Uses = outputUses[0]
		}
		p.Root = &plan.SourceNode{QuerySchema: plan.QuerySchema(d.Columns), Ref: b.Ref, Occurrence: 0}
		var from plan.Node
		var condition expr.Expr
		if event == "UPDATE" {
			from = b.Update.From
			condition = b.Update.Predicate
			for i, slot := range b.Targets {
				value := b.Update.Assignments[i]
				if len(b.Update.ArrayTargets) > i && b.Update.ArrayTargets[i] != nil {
					value = plan.AssignmentValueExpr{Info: expr.Info{Usage: d.Columns[slot].Usage, Pos: value.SourcePosition(), Property: expr.MergeProperties(p.New[slot].Properties(), value.Properties())}, Base: p.New[slot], Value: value, Target: b.Update.ArrayTargets[i]}
				}
				p.New[slot] = value
			}
		} else {
			from = b.Delete.Using
			condition = b.Predicate
		}
		mapper := func(bound expr.Expr) (expr.Expr, bool, error) {
			return stored.RewriteViewExpression(catalog, bound, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
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
		for i, expr := range p.New {
			var err error
			p.New[i], _, err = mapper(expr)
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
			p.Root = ruleJoin(p.Root, from)
		}
		if condition != nil {
			var err error
			condition, _, err = mapper(condition)
			if err != nil {
				return p, err
			}
			p.Root = &plan.FilterNode{QuerySchema: plan.QuerySchema(p.Root.Schema()), Child: p.Root, Predicate: condition}
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
	if err := table.ValidateColumnDefaultProviders(view, parent, definition); err != nil {
		return err
	}
	var failure error
	record := func(captured *catalog.Procedure) {
		if failure != nil || captured == nil {
			return
		}
		current, err := exec.ValidateStoredRoutineDescriptor(view, captured)
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
			if !stored.ReferenceEpochValid(view, parent, original) {
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
		captured, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil {
			failure = err
			return
		}
		record(captured)
		// Keep the original call's provenance failure: a bodyless builtin's
		// successful body walk cannot certify a missing or stale outer call.
		if captured != nil && failure == nil {
			failure = exec.WalkRoutineBody(captured, node, func(body *catalog.Procedure) error {
				current, ok := view.Procedure(body.Address())
				if !ok || !exec.SameRoutineDeclaration(current, body) {
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

func normalizeRuleInsert(b analyze.Binding) analyze.Binding {
	if b.Query != nil {
		return b
	}
	schema := make(plan.QuerySchema, len(b.Targets))
	for i, slot := range b.Targets {
		schema[i] = b.Relation.Columns[slot]
		if i < len(b.ArrayTargets) && b.ArrayTargets[i] != nil {
			schema[i] = b.ArrayTargets[i].Column(schema[i])
		}
	}
	rows := ruleInsertDefaultRows(b, schema)
	if len(rows) == 1 {
		b.Query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: schema, Child: &plan.SingletonNode{}, Expressions: rows[0], Frame: 1}}
	} else {
		projection := make([]expr.Expr, len(schema))
		for i, column := range schema {
			projection[i] = plan.ColumnExpr{Info: RuleColumnInfo(column), Scope: 1, Frame: 1, Slot: i}
		}
		b.Query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: schema, Child: &plan.ValuesNode{QuerySchema: schema, Rows: rows}, Expressions: projection, Frame: 1}}
	}
	b.CommandQuery = true
	b.Expressions = nil
	return b
}

func RuleSetAction(b analyze.Binding, syntax parser.Statement) bool {
	if _, selecting := syntax.(*parser.Select); !selecting || b.Query == nil {
		return false
	}
	_, set := ruleQueryInput(b.Query.Root).(*plan.SetNode)
	return set
}

func ComposeRuleAction(auth RuleAuthority, original analyze.Binding, event string, r stored.Rule, action stored.RuleAction) (analyze.Binding, error) {
	qualified := original.Predicate != nil || original.Update != nil && original.Update.Predicate != nil
	if (qualified || r.Condition() != nil) && RuleSetAction(action.Binding(), action.Syntax()) {
		return analyze.Binding{}, sqlError("0A000", "conditional UNION/INTERSECT/EXCEPT statements are not implemented")
	}
	catalog := auth.Access().Catalog
	p, err := MakeRuleProducer(catalog, original, event, ruleProducerOutputUses(original, event, r, action.Binding()))
	if err != nil {
		return analyze.Binding{}, err
	}
	// Unqualified UPDATE/DELETE actions that do not reference the event
	// namespace run independently of the event table's physical row count.
	// INSERT retains its producer cardinality even without NEW demand.
	if event != "INSERT" && !qualified && r.Condition() == nil && !ruleBindingHasPseudo(action.Binding()) {
		p.Root, p.Sources = &plan.SingletonNode{}, nil
	}
	// Resolve authority at the retained producer boundary, before switching
	// to the event owner's context for stored action refresh and composition.
	caller := auth.Access().State.CurrentUser
	auth = auth.AsUser(original.Relation.Owner)
	b := action.Binding()
	dependenciesPrivate := false
	b.Insert.Conflict = b.Insert.Conflict.Clone()
	b, err = RefreshRuleActionCTEs(auth, b)
	if err != nil {
		return b, err
	}
	if b.Relation != nil {
		current, ok := catalog.Relation(b.Ref.Address)
		if !ok {
			return b, ids.ErrCatalogReplaced
		}
		b, err = stored.RefreshRuleBinding(catalog, b, current)
		if err != nil {
			return b, err
		}
	}
	b, err = RefreshRuleSources(auth, b)
	if err != nil {
		return b, err
	}
	if insert, ok := action.Syntax().(*parser.Insert); ok {
		// Current destination defaults must be selected before DEFAULT cells
		// become query outputs, not from the stored action's older schedule.
		b.Insert.Defaults, b.Insert.DefaultOrigins = nil, nil
		analyze.BindInsertDefaults(auth.Access().Owner, &b, catalog, insert.Override)
		b = normalizeRuleInsert(b)
	}
	b, err = IsolateRuleCTEFrames(catalog, original, b, action.Syntax())
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
		p.Root, _, err = rewriteRuleFrames(catalog, p.Root, nil, rootFrame, original.CTEs)
		if err != nil {
			return b, err
		}
		for i, expr := range p.old {
			_, p.old[i], err = rewriteRuleFrames(catalog, nil, expr, frame)
			if err != nil {
				return b, err
			}
		}
		for i, expr := range p.New {
			_, p.New[i], err = rewriteRuleFrames(catalog, nil, expr, frame)
			if err != nil {
				return b, err
			}
		}
	}
	width := len(p.Root.Schema())
	targetWidth := 0
	if _, ok := action.Syntax().(*parser.Update); ok {
		targetWidth = len(b.Relation.Columns)
	}
	if _, ok := action.Syntax().(*parser.Delete); ok {
		targetWidth = len(b.Relation.Columns)
	}
	sourceOffset := len(p.Sources)
	b, err = OffsetRuleSources(catalog, b, sourceOffset, original.CTEs)
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
				recipes = p.New
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
				if err := ValidateRuleNewUpdateOutput(recipe); err != nil {
					return nil, false, err
				}
			}
			if targetOffset != 0 {
				var err error
				recipe, _, err = stored.RewriteViewExpression(catalog, recipe, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
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
	tree := stored.NewRuleTreeMapper(catalog, nil, 0, original.CTEs, stored.RuleTreeMapping{
		Leaf:   substitute,
		Finish: finish,
		Boundary: func(node plan.Node) (plan.Node, bool) {
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
		return tree.Expression(expr)
	}
	mapQuery := func(root plan.Node, input bool) (plan.Node, error) {
		previousOffset, previousShift := targetOffset, shift
		targetOffset, shift = targetWidth, true
		if input {
			targetOffset, shift = 0, false
		}
		defer func() { targetOffset, shift = previousOffset, previousShift }()
		return tree.Query(root)
	}
	mapTargets := func(targets []*plan.ArrayAssignmentTarget) ([]*plan.ArrayAssignmentTarget, error) {
		return stored.MapRuleAssignmentTargets(targets, mapper, func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil })
	}
	b.ArrayTargets, err = mapTargets(b.ArrayTargets)
	if err != nil {
		return b, err
	}
	condition := r.Condition()
	var conditionSources []exec.Leaf
	if r.ConditionBinding() != nil {
		cb, refreshErr := RefreshRuleSources(auth, *r.ConditionBinding())
		if refreshErr != nil {
			return b, refreshErr
		}
		cb, err = OffsetRuleSources(catalog, cb, sourceOffset+len(b.QuerySources))
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
			return p.Root, nil
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
				projection[i] = plan.ColumnExpr{Info: RuleColumnInfo(column), Scope: 1, Frame: innerFrame, Slot: i}
			}
			right = &plan.ProjectNode{QuerySchema: plan.QuerySchema(right.Schema()), Child: right, Expressions: projection, Frame: innerFrame, RuleInvocation: true}
		} else {
			right, err = mapQuery(right, true)
			if err != nil {
				return nil, err
			}
		}
		joined := ruleJoin(p.Root, right).(*plan.JoinNode)
		joined.Frame = actionFrame
		joined.CorrelatedRight = correlated
		var replacement plan.Node = joined
		if condition != nil && targetWidth == 0 {
			replacement = &plan.FilterNode{QuerySchema: plan.QuerySchema(joined.Schema()), Child: joined, Predicate: condition}
		}
		patched, err := RuleGraftSchemas(root, input, replacement, windowSlots)
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
			u.Predicate = ViewWritePredicate([]expr.Expr{condition}, u.Predicate)
		}
		u.From, err = graft(u.From)
		if err != nil {
			return b, err
		}
		u.SourceWidth = len(u.From.Schema())
		b.Update = &u
	}
	if _, ok := action.Syntax().(*parser.Delete); ok {
		b.Predicate, _, err = mapper(b.Predicate)
		if err != nil {
			return b, err
		}
		if condition != nil {
			b.Predicate = ViewWritePredicate([]expr.Expr{condition}, b.Predicate)
		}
		b.Delete.Using, err = graft(b.Delete.Using)
		if err != nil {
			return b, err
		}
		b.Delete.SourceWidth = len(b.Delete.Using.Schema())
	}
	var definitions []*plan.CTEDefinition
	for i, definition := range b.CTEs {
		mapped, mapErr := tree.Definition(definition)
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
	b.QuerySources = append(slices.Clone(p.Sources), b.QuerySources...)
	b.QuerySources = append(b.QuerySources, conditionSources...)
	for i := range b.QuerySources {
		if i < sourceOffset {
			if b.QuerySources[i].Principal == (authorization.PrincipalRef{}) {
				b.QuerySources[i].Principal = caller
			}
		} else {
			b.QuerySources[i].Principal = auth.Access().State.CurrentUser
		}
	}
	b.SubqueryDemand = b.SubqueryDemand || original.SubqueryDemand
	b.Dependencies = append(slices.Clone(original.Dependencies), b.Dependencies...)
	b.Parameters = original.Parameters
	return b, nil
}
