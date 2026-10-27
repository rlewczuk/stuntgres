package core

import (
	"fmt"
	"maps"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func (r *explainRenderer) command(statement parser.Statement, bound explainBound) ([]string, error) {
	var trees []*explainTree
	if bound.Rules != nil {
		if err := analyze.CheckBinding(r.auth, bound.Binding, statement); err != nil {
			return nil, err
		}
		if len(bound.Rules.Commands) == 0 {
			trees = append(trees, &explainTree{label: "Rewrite: nothing"})
		}
		for _, command := range bound.Rules.Commands {
			auth := r.auth
			r.auth = ruleAuthorizationOf(command.Auth)
			tree, err := r.binding(command.Syntax, command.Binding)
			r.auth = auth
			if err != nil {
				return nil, err
			}
			tree.details = append([]string{"Rule rewrite command"}, tree.details...)
			trees = append(trees, tree)
		}
		if len(bound.Rules.Commands) != 0 {
			rewrite := &explainTree{label: "Rule Rewrite", children: trees}
			for i, command := range trees {
				rewrite.estimate.Cost = min(plan.ExplainEstimateLimit, rewrite.estimate.Cost+command.estimate.Cost)
				if i == bound.Rules.Returning {
					rewrite.estimate.Rows = command.estimate.Rows
				}
			}
			trees = []*explainTree{rewrite}
		}
	} else {
		tree, err := r.binding(statement, bound.Binding)
		if err != nil {
			return nil, err
		}
		trees = append(trees, tree)
	}
	// Expressions discover subplans in lexical presentation order. Processing
	// may discover nested subplans; the dynamic length deliberately includes them.
	for i := 0; i < len(r.subplans); i++ {
		subplan := r.subplans[i]
		frames, occurrences, auth, leaves := r.frames, r.occurrences, r.auth, r.leaves
		image, target := r.image, r.policyTarget
		r.image, r.policyTarget = subplan.image, subplan.policyTarget
		r.frames, r.occurrences, r.auth, r.leaves = maps.Clone(subplan.frames), maps.Clone(subplan.occurrences), subplan.auth, subplan.leaves
		tree, err := r.query(subplan.query)
		r.frames, r.occurrences, r.auth, r.leaves = frames, occurrences, auth, leaves
		r.image, r.policyTarget = image, target
		if err != nil {
			return nil, err
		}
		trees = append(trees, &explainTree{label: subplan.label, children: []*explainTree{tree}, estimate: tree.estimate})
	}
	trees = append(trees, r.producers...)
	if len(trees) != 0 {
		total := trees[0].estimate
		for _, auxiliary := range trees[1:] {
			total.Cost = min(plan.ExplainEstimateLimit, total.Cost+auxiliary.estimate.Cost)
		}
		trees[0].estimate = total
	}
	var lines []string
	for _, tree := range trees {
		r.lines(tree, 0, &lines)
	}
	if r.options.costs {
		lines = append(lines, "Estimates: logical heuristics; not execution measurements")
	}
	return lines, nil
}

func (r *explainRenderer) cte(definition *plan.CTEDefinition) (*explainTree, error) {
	if tree, ok := r.ctes[definition]; ok {
		return tree, nil
	}
	tree := &explainTree{label: "CTE " + catalog.Identifier(definition.Name)}
	r.ctes[definition] = tree
	frames, occurrences, auth := r.frames, r.occurrences, r.auth
	r.frames, r.occurrences = maps.Clone(frames), maps.Clone(occurrences)
	defer func() { r.frames, r.occurrences, r.auth = frames, occurrences, auth }()
	var child *explainTree
	var err error
	if rewrite := r.rewrites[definition]; rewrite != nil {
		if mutation, ok := analyze.MutationBinding(definition); ok {
			if err := analyze.CheckBinding(ruleAuthorizationOf(rewrite.Caller), *mutation, definition.Statement); err != nil {
				return nil, err
			}
		}
		r.auth = ruleAuthorizationOf(rewrite.Command.Auth)
		child, err = r.binding(rewrite.Command.Syntax, rewrite.Command.Binding)
	} else if mutation, ok := definition.Mutation.(*analyze.Binding); ok {
		child, err = r.binding(definition.Statement, *mutation)
	} else {
		child, err = r.query(definition.Query)
	}
	if err != nil {
		return nil, err
	}
	tree.children, tree.estimate = []*explainTree{child}, child.estimate
	r.producers = append(r.producers, tree)
	return tree, nil
}

func (r *explainRenderer) binding(statement parser.Statement, binding analyze.Binding) (*explainTree, error) {
	if err := analyze.CheckBinding(r.auth, binding, statement); err != nil {
		return nil, err
	}
	leaves := r.leaves
	r.leaves = binding.QuerySources
	defer func() { r.leaves = leaves }()
	if rewrites := view.RuleCTEsOf(binding.Rule.CTEs); len(rewrites) != 0 {
		if r.rewrites == nil {
			r.rewrites = make(view.RuleCTECommands)
		}
		maps.Copy(r.rewrites, rewrites)
	}
	for _, definition := range binding.CTEs {
		if definition.Mutation != nil {
			if _, err := r.cte(definition); err != nil {
				return nil, err
			}
		}
	}
	if _, selectStatement := statement.(*parser.Select); selectStatement {
		return r.query(binding.Query)
	}
	original := binding.Relation
	binding = view.PhysicalBinding(binding)
	if binding.Relation == nil {
		return nil, fmt.Errorf("stuntgres: missing EXPLAIN mutation target")
	}
	target := &plan.SourceNode{QuerySchema: plan.QuerySchema(binding.Relation.Columns), Ref: binding.Ref, Occurrence: -1, RangeName: binding.Relation.Name()}
	policyTarget, policyCommand, targetOnly := r.policyTarget, r.policyCommand, r.targetOnly
	defer func() { r.policyTarget, r.policyCommand, r.targetOnly = policyTarget, policyCommand, targetOnly }()
	reads, returning, principal := r.policyReads, r.policyReturning, r.policyPrincipal
	defer func() { r.policyReads, r.policyReturning, r.policyPrincipal = reads, returning, principal }()
	r.policyReads, r.policyReturning = binding.Returning != nil || len(binding.UsedColumns) != 0, binding.Returning != nil
	if binding.ViewWrite != nil {
		r.policyReads = r.policyReads || len(view.ViewWriteOf(binding.ViewWrite).Logical.UsedColumns) != 0
	}
	r.policyPrincipal = access.RowSecurityPrincipal(r.auth.State.CurrentUser, view.RowSecurityLayers(r.auth.Catalog, binding))
	r.policyTarget = binding.Ref.Address
	r.policyCommand = catalog.PolicySelect
	r.targetOnly = false
	switch syntax := statement.(type) {
	case *parser.Update:
		r.policyCommand, r.targetOnly = catalog.PolicyUpdate, syntax.Only
	case *parser.Delete:
		r.policyCommand, r.targetOnly = catalog.PolicyDelete, syntax.Only
	}
	var source plan.Node
	var predicate expr.Expr
	label := ""
	switch syntax := statement.(type) {
	case *parser.Insert:
		label = "Insert"
		if syntax.Alias.Text != "" {
			target.RangeName = syntax.Alias.Text
		}
		if binding.Query != nil {
			source = binding.Query.Root
		} else if len(binding.Expressions) != 0 {
			source = &plan.ValuesNode{Rows: binding.Expressions}
		} else {
			source = &plan.SingletonNode{}
		}
		if syntax.DefaultValues {
			label += " DEFAULT VALUES"
		}
	case *parser.Update:
		label, source, predicate = "Update", target, binding.Predicate
		if syntax.Alias.Text != "" {
			target.RangeName = syntax.Alias.Text
		}
		if binding.Update != nil {
			predicate = binding.Update.Predicate
			source = explainCandidateJoin(target, binding.Update.From, nil, parser.JoinInner)
		}
	case *parser.Delete:
		label, predicate = "Delete", binding.Predicate
		if syntax.Alias.Text != "" {
			target.RangeName = syntax.Alias.Text
		}
		source = explainCandidateJoin(target, binding.Delete.Using, nil, parser.JoinInner)
	case *parser.Merge:
		label = "Merge"
		if syntax.Alias.Text != "" {
			target.RangeName = syntax.Alias.Text
		}
		if binding.Merge == nil {
			return nil, fmt.Errorf("stuntgres: missing EXPLAIN MERGE binding")
		}
		kind := parser.JoinInner
		if binding.Merge.HasBySource && binding.Merge.HasByTarget {
			kind = parser.JoinFull
		} else if binding.Merge.HasBySource {
			kind = parser.JoinLeft
		} else if binding.Merge.HasByTarget {
			kind = parser.JoinRight
		}
		source = explainCandidateJoin(target, binding.Merge.Source, binding.Merge.On, kind)
	default:
		return nil, fmt.Errorf("stuntgres: invalid EXPLAIN mutation binding")
	}
	if predicate != nil {
		source = &plan.FilterNode{QuerySchema: plan.QuerySchema(source.Schema()), Child: source, Predicate: predicate}
	}
	r.register(source)
	// Mutation expressions use the same target-plus-FROM candidate frame as
	// their binder, not a fabricated independent SELECT projection.
	r.frames[1] = source
	var child *explainTree
	var err error
	if binding.CurrentOf != "" {
		child = &explainTree{label: "Cursor Locator on " + r.objectName(binding.Relation.Namespace(), binding.Relation.Name()), estimate: plan.ExplainEstimate{Rows: 1, Cost: 1}}
		if err = r.sourceDetails(child, target, binding.Relation); err != nil {
			return nil, err
		}
		if child.restriction != nil {
			child.estimate, err = plan.EstimateExplainNode(&plan.FilterNode{Predicate: child.restriction}, []plan.ExplainEstimate{child.estimate}, 0)
		}
	} else {
		child, err = r.node(source)
	}
	if err != nil {
		return nil, err
	}
	tree := &explainTree{label: label + " on " + r.objectName(binding.Relation.Namespace(), binding.Relation.Name()), children: []*explainTree{child}, estimate: plan.ExplainMutationEstimate(child.estimate, binding.Returning != nil)}
	if original != binding.Relation {
		tree.details = append(tree.details, "Writable View: "+r.objectName(original.Namespace(), original.Name()))
	}
	if r.options.costs {
		tree.details = append(tree.details, fmt.Sprintf("Estimated candidates: %.0f", child.estimate.RoundedRows()))
	}
	if binding.CurrentOf != "" {
		tree.details = append(tree.details, "CURRENT OF: "+catalog.Identifier(binding.CurrentOf))
	}
	if len(binding.Targets) != 0 {
		var targets []string
		for _, index := range binding.Targets {
			targets = append(targets, catalog.Identifier(binding.Relation.Columns[index].Name))
		}
		tree.details = append(tree.details, "Target Columns: "+strings.Join(targets, ", "))
	}
	for i, expression := range binding.Insert.Defaults {
		if expression == nil {
			continue
		}
		text, err := r.expression(source, expression)
		if err != nil {
			return nil, err
		}
		name := fmt.Sprintf("column %d", i+1)
		if i < len(binding.Relation.Columns) {
			name = catalog.Identifier(binding.Relation.Columns[i].Name)
		}
		tree.details = append(tree.details, "DEFAULT "+name+": "+text)
	}
	if binding.Update != nil {
		if err := r.assignments(tree, source, binding.Relation, binding.Targets, binding.Update); err != nil {
			return nil, err
		}
	}
	if conflict := binding.Insert.Conflict; conflict != nil {
		tree.details = append(tree.details, "On Conflict: DO NOTHING")
		if conflict.Update != nil {
			tree.details[len(tree.details)-1] = "On Conflict: DO UPDATE"
			conflictSource := explainCandidateJoin(target, &plan.SourceNode{QuerySchema: target.QuerySchema, Ref: binding.Ref, Occurrence: -1, RangeName: "excluded"}, nil, parser.JoinInner)
			frames := r.frames
			r.frames = maps.Clone(frames)
			r.frames[1] = conflictSource
			err := r.assignments(tree, conflictSource, binding.Relation, conflict.UpdateTargets, conflict.Update)
			r.frames = frames
			if err != nil {
				return nil, err
			}
		}
		for _, key := range conflict.Keys {
			text := ""
			if key.Expression != nil {
				text, err = r.expression(target, key.Expression)
			} else {
				text, err = storedAttribute(binding.Relation, key.Attribute)
			}
			if err != nil {
				return nil, err
			}
			tree.details = append(tree.details, "Conflict Arbiter: "+text)
		}
		if conflict.Constraint.ObjectID != 0 {
			if declaration, ok := r.auth.Catalog.Find(conflict.Constraint); ok {
				tree.details = append(tree.details, "Conflict Constraint: "+catalog.Identifier(declaration.Name()))
			}
		}
		if conflict.Predicate != nil {
			text, err := r.expression(target, conflict.Predicate)
			if err != nil {
				return nil, err
			}
			tree.details = append(tree.details, "Conflict Predicate: "+text)
		}
	}
	if binding.Merge != nil {
		for _, arm := range binding.Merge.Arms {
			match := "NOT MATCHED BY TARGET"
			if arm.Match == parser.MergeMatched {
				match = "MATCHED"
			} else if arm.Match == parser.MergeNotMatchedBySource {
				match = "NOT MATCHED BY SOURCE"
			}
			if arm.Condition != nil {
				condition, err := r.expression(source, arm.Condition)
				if err != nil {
					return nil, err
				}
				match += " AND " + condition
			}
			action := map[parser.MergeAction]string{parser.MergeInsertAction: "INSERT", parser.MergeUpdateAction: "UPDATE", parser.MergeDeleteAction: "DELETE", parser.MergeNothingAction: "DO NOTHING"}[arm.Action]
			tree.details = append(tree.details, "When "+match+" THEN "+action)
			if arm.Input.Update != nil {
				if err := r.assignments(tree, source, binding.Relation, arm.Input.Targets, arm.Input.Update); err != nil {
					return nil, err
				}
			}
			for _, row := range arm.Input.Expressions {
				text, err := r.valueRow(source, row)
				if err != nil {
					return nil, err
				}
				tree.details = append(tree.details, "Insert Values: "+text)
			}
			if arm.Action == parser.MergeInsertAction || arm.Action == parser.MergeUpdateAction {
				command := catalog.PolicyInsert
				if arm.Action == parser.MergeUpdateAction {
					command = catalog.PolicyUpdate
				}
				if err := r.rowSecurityCheck(tree, target, binding.Relation, command); err != nil {
					return nil, err
				}
			}
		}
	}
	switch statement.(type) {
	case *parser.Insert:
		if err := r.rowSecurityCheck(tree, target, binding.Relation, catalog.PolicyInsert); err != nil {
			return nil, err
		}
	case *parser.Update:
		if err := r.rowSecurityCheck(tree, target, binding.Relation, catalog.PolicyUpdate); err != nil {
			return nil, err
		}
	}
	if binding.Returning != nil {
		returningSource := source
		if _, insert := statement.(*parser.Insert); insert {
			// INSERT's input query is not its produced target row.
			returningSource = target
		}
		// UPDATE/DELETE/MERGE retain the candidate's combined source frame,
		// including the lexical owner captured by RETURNING subplans.
		r.frames[1] = returningSource
		text, err := r.expressions(returningSource, binding.Returning.Expressions)
		if err != nil {
			return nil, err
		}
		tree.details = append(tree.details, "RETURNING: "+text)
	}
	return tree, nil
}

func explainCandidateJoin(target, source plan.Node, predicate expr.Expr, kind parser.JoinKind) plan.Node {
	if source == nil {
		return target
	}
	schema := append(append(plan.QuerySchema(nil), target.Schema()...), source.Schema()...)
	return &plan.JoinNode{QuerySchema: schema, Kind: kind, Left: target, Right: source, Predicate: predicate, LeftWidth: len(target.Schema()), RightWidth: len(source.Schema()), Frame: 1}
}

func storedAttribute(relation *catalog.Relation, attribute int32) (string, error) {
	for i, current := range relation.Attributes {
		if current == attribute {
			return catalog.Identifier(relation.Columns[i].Name), nil
		}
	}
	return "", fmt.Errorf("stuntgres: missing EXPLAIN attribute")
}

func (r *explainRenderer) assignments(tree *explainTree, source plan.Node, relation *catalog.Relation, targets []int, update *analyze.UpdateBinding) error {
	roots := r.assignmentRoots
	r.assignmentRoots = update.Assignments
	defer func() { r.assignmentRoots = roots }()
	for i, expression := range update.Assignments {
		if i >= len(targets) || targets[i] < 0 || targets[i] >= len(relation.Columns) {
			return fmt.Errorf("stuntgres: invalid EXPLAIN assignment target")
		}
		name := catalog.Identifier(relation.Columns[targets[i]].Name)
		if i < len(update.ArrayTargets) && update.ArrayTargets[i] != nil {
			text, err := r.assignmentTarget(source, name, update.ArrayTargets[i])
			if err != nil {
				return err
			}
			name = text
		}
		text := "DEFAULT"
		if expression != nil {
			var err error
			text, err = r.expression(source, expression)
			if err != nil {
				return err
			}
		} else {
			defaultExpression, _ := analyze.ColumnDefault(r.auth.Catalog, relation, targets[i])
			if generation, ok := relation.ColumnMeta.Generations[relation.Attributes[targets[i]]]; ok {
				defaultExpression = generation.Planned
			}
			if defaultExpression != nil {
				defaultSQL, err := r.expression(source, defaultExpression)
				if err != nil {
					return err
				}
				tree.details = append(tree.details, "DEFAULT "+name+": "+defaultSQL)
			}
		}
		tree.details = append(tree.details, "Assignment: "+name+" = "+text)
	}
	if update.Predicate != nil {
		text, err := r.expression(source, update.Predicate)
		if err != nil {
			return err
		}
		tree.details = append(tree.details, "Action Filter: "+text)
	}
	return nil
}

func (r *explainRenderer) rowSecurityCheck(tree *explainTree, target plan.Node, relation *catalog.Relation, command catalog.PolicyCommand) error {
	reads := r.policyReads
	if command == catalog.PolicyInsert {
		reads = r.policyReturning
	}
	condition, err := r.rowSecurity(relation, command, true, reads, r.policyPrincipal)
	if err != nil || condition == nil {
		return err
	}
	frames := r.frames
	r.frames = maps.Clone(frames)
	r.frames[1] = target
	text, err := r.expression(target, condition)
	r.frames = frames
	if err == nil {
		tree.details = append(tree.details, "Row Security Check: "+text)
	}
	return err
}

func (r *explainRenderer) assignmentTarget(source plan.Node, name string, target *plan.ArrayAssignmentTarget) (string, error) {
	for partial := target; partial != nil; partial = partial.Next {
		if partial.Attribute != 0 {
			name += "." + catalog.Identifier(partial.Name)
		}
		for _, subscript := range partial.Node.Subscripts {
			lower, upper := "", ""
			var err error
			if subscript.Lower != nil {
				lower, err = r.expression(source, subscript.Lower)
				if err != nil {
					return "", err
				}
			}
			if subscript.Upper != nil {
				upper, err = r.expression(source, subscript.Upper)
				if err != nil {
					return "", err
				}
			}
			if subscript.Slice {
				name += "[" + lower + ":" + upper + "]"
			} else {
				name += "[" + upper + "]"
			}
		}
	}
	return name, nil
}
