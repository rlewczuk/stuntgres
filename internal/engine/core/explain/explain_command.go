package explain

import (
	"database/sql/driver"
	"fmt"
	"maps"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Bound graphs are borrowed only by this Describe/Execute activation. They are
// deliberately not part of Description, a portal, or the prepared fold owner.
type Bound struct {
	Binding analyze.Binding
	Rules   *view.RuleProgram
}

func (r *Renderer) Command(statement parser.Statement, bound Bound) ([]string, error) {
	var trees []*Tree
	if bound.Rules != nil {
		if err := analyze.CheckBinding(r.auth.full, bound.Binding, statement); err != nil {
			return nil, err
		}
		if len(bound.Rules.Commands) == 0 {
			tree := &Tree{label: "Rewrite: nothing", nodeType: "Rewrite"}
			r.property(tree, "Action", "nothing")
			trees = append(trees, tree)
		}
		for _, command := range bound.Rules.Commands {
			auth := r.auth
			r.auth = authorityOf(ruleAuthority(command.Auth))
			tree, err := r.binding(command.Syntax, command.Binding)
			r.auth = auth
			if err != nil {
				return nil, err
			}
			tree.details = append([]string{"Rule rewrite command"}, tree.details...)
			r.property(tree, "Rule Rewrite Command", true)
			trees = append(trees, tree)
		}
		if len(bound.Rules.Commands) != 0 {
			rewrite := &Tree{label: "Rule Rewrite", nodeType: "Rule Rewrite", children: trees}
			for i, command := range trees {
				rewrite.estimate.Cost = min(plan.ExplainEstimateLimit, rewrite.estimate.Cost+command.estimate.Cost)
				if i == bound.Rules.Returning {
					rewrite.estimate.Rows = command.estimate.Rows
				}
			}
			trees = []*Tree{rewrite}
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
		foreign, marked, inherited := r.foreign, r.rowMarked, r.inheritedMark
		owners := r.foreignOwners
		r.foreignOwners = maps.Clone(subplan.foreignOwners)
		r.foreign, r.rowMarked, r.inheritedMark = subplan.foreign, subplan.rowMarked, subplan.inheritedMark
		r.image, r.policyTarget = subplan.image, subplan.policyTarget
		r.frames, r.occurrences, r.auth, r.leaves = maps.Clone(subplan.frames), maps.Clone(subplan.occurrences), subplan.auth, subplan.leaves
		tree, err := r.query(subplan.query)
		r.frames, r.occurrences, r.auth, r.leaves = frames, occurrences, auth, leaves
		r.image, r.policyTarget = image, target
		r.foreign, r.rowMarked, r.inheritedMark = foreign, marked, inherited
		r.foreignOwners = owners
		if err != nil {
			return nil, err
		}
		wrapper := &Tree{label: subplan.label, nodeType: "SubPlan", children: []*Tree{tree}, estimate: tree.estimate}
		r.property(wrapper, "Subplan Name", subplan.label)
		trees = append(trees, wrapper)
	}
	trees = append(trees, r.producers...)
	if len(trees) != 0 {
		total := trees[0].estimate
		for _, auxiliary := range trees[1:] {
			total.Cost = min(plan.ExplainEstimateLimit, total.Cost+auxiliary.estimate.Cost)
		}
		trees[0].estimate = total
	}
	if r.options.Debug {
		numberDebug(trees)
	}
	if err := r.captureRanges(statement, bound); err != nil {
		return nil, err
	}
	r.trees = trees
	if r.Observation != nil || r.options.Structured() {
		return nil, nil
	}
	return r.Render(), nil
}

// SetForeignParameters supplies the statement's typed parameter frame and its
// provider export for foreign planning.
func (r *Renderer) SetForeignParameters(export exec.ForeignValueExporter, typed []sqltype.Value, values []driver.Value) {
	r.exportValue, r.typedParameters, r.parameters.Values = export, typed, values
}

// CommandTree is the command's presentation root, or nil when it has none.
func (r *Renderer) CommandTree() *Tree {
	if len(r.trees) == 0 {
		return nil
	}
	return r.trees[0]
}

// MarkRuleRewrite labels the command as a rule's rewritten action.
func (r *Renderer) MarkRuleRewrite() {
	if len(r.trees) == 0 {
		return
	}
	r.trees[0].details = append([]string{"Rule rewrite command"}, r.trees[0].details...)
	r.property(r.trees[0], "Rule Rewrite Command", true)
}

// PresentedNodes lists borrowed plan identities in presentation order for core's
// actual-owner tests and ExplainObservationForTest (the recorded T6 seam).
func (r *Renderer) PresentedNodes() []plan.Node {
	var nodes []plan.Node
	var visit func(*Tree)
	visit = func(tree *Tree) {
		if tree.node != nil {
			nodes = append(nodes, tree.node)
		}
		for _, child := range tree.children {
			visit(child)
		}
	}
	for _, tree := range r.trees {
		visit(tree)
	}
	return nodes
}

// Render returns the TEXT lines of the presentation.
func (r *Renderer) Render() []string {
	var lines []string
	for _, tree := range r.trees {
		r.lines(tree, 0, &lines)
	}
	if r.rangeTable != nil {
		lines = append(lines, "Embedded Range Table:")
		rangeLines(r.rangeTable, "  ", "", &lines)
	}
	if r.options.Costs {
		lines = append(lines, "Estimates: logical heuristics; not execution measurements")
	}
	if r.Observation != nil {
		lines = append(lines, "Measurements: emitted rows and active elapsed milliseconds per loop; elapsed includes child work, sums parallel worker plus leader work, excludes consumer idle time and transaction publication")
	}
	return lines
}

func (r *Renderer) cte(definition *plan.CTEDefinition) (*Tree, error) {
	if tree, ok := r.ctes[definition]; ok {
		return tree, nil
	}
	tree := &Tree{label: "CTE " + catalog.Identifier(definition.Name), nodeType: "CTE"}
	r.property(tree, "CTE Name", definition.Name)
	r.ctes[definition] = tree
	foreign := r.foreign
	if owner, ok := r.foreignOwners[definition.Owner]; ok {
		r.foreign = owner
	}
	defer func() { r.foreign = foreign }()
	frames, occurrences, auth := r.frames, r.occurrences, r.auth
	r.frames, r.occurrences = maps.Clone(frames), maps.Clone(occurrences)
	defer func() { r.frames, r.occurrences, r.auth = frames, occurrences, auth }()
	var child *Tree
	var err error
	if rewrite := r.rewrites[definition]; rewrite != nil {
		if mutation, ok := analyze.MutationBinding(definition); ok {
			if err := analyze.CheckBinding(ruleAuthority(rewrite.Caller), *mutation, definition.Statement); err != nil {
				return nil, err
			}
		}
		r.auth = authorityOf(ruleAuthority(rewrite.Command.Auth))
		child, err = r.binding(rewrite.Command.Syntax, rewrite.Command.Binding)
	} else if mutation, ok := definition.Mutation.(*analyze.Binding); ok {
		child, err = r.binding(definition.Statement, *mutation)
	} else {
		child, err = r.query(definition.Query)
	}
	if err != nil {
		return nil, err
	}
	tree.children, tree.estimate = []*Tree{child}, child.estimate
	r.producers = append(r.producers, tree)
	return tree, nil
}

func (r *Renderer) binding(statement parser.Statement, binding analyze.Binding) (*Tree, error) {
	if err := analyze.CheckBinding(r.auth.full, binding, statement); err != nil {
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
	// These adapters describe DML candidate frames; they are not bound nodes.
	// Keep their identities out of DEBUG without a graph registry/allocation.
	var adapters [3]plan.Node // target, candidate join, predicate; INSERT needs only its literal adapter.
	synthetic := adapters[:0]
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
		if r.options.Debug && binding.Query == nil {
			synthetic = append(synthetic, source)
		}
		if syntax.DefaultValues {
			label += " DEFAULT VALUES"
		}
	case *parser.Update:
		label, source, predicate = "Update", target, binding.Predicate
		if r.options.Debug {
			synthetic = append(synthetic, target)
		}
		if syntax.Alias.Text != "" {
			target.RangeName = syntax.Alias.Text
		}
		if binding.Update != nil {
			predicate = binding.Update.Predicate
			source = explainCandidateJoin(target, binding.Update.From, nil, parser.JoinInner)
			if r.options.Debug && source != target {
				synthetic = append(synthetic, source)
			}
		}
	case *parser.Delete:
		label, predicate = "Delete", binding.Predicate
		if syntax.Alias.Text != "" {
			target.RangeName = syntax.Alias.Text
		}
		source = explainCandidateJoin(target, binding.Delete.Using, nil, parser.JoinInner)
		if r.options.Debug {
			synthetic = append(synthetic, target)
			if source != target {
				synthetic = append(synthetic, source)
			}
		}
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
		if r.options.Debug {
			synthetic = append(synthetic, target, source)
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid EXPLAIN mutation binding")
	}
	if predicate != nil {
		source = &plan.FilterNode{QuerySchema: plan.QuerySchema(source.Schema()), Child: source, Predicate: predicate}
		if r.options.Debug {
			synthetic = append(synthetic, source)
		}
	}
	r.register(source)
	// Mutation expressions use the same target-plus-FROM candidate frame as
	// their binder, not a fabricated independent SELECT projection.
	r.frames[1] = source
	identity := r.modifyIdentity
	r.modifyIdentity = nil
	defer func() { r.modifyIdentity = identity }()
	modificationTree := &Tree{}
	if binding.Relation.Foreign != nil && r.Observation == nil {
		var operation catalog.ForeignModifyOperation
		supported := true
		switch statement.(type) {
		case *parser.Insert:
			operation = catalog.ForeignInsert
		case *parser.Update:
			operation = catalog.ForeignUpdate
		case *parser.Delete:
			operation = catalog.ForeignDelete
		default:
			supported = false
		}
		if supported {
			if err := r.foreignModify(modificationTree, binding.Relation, operation); err != nil {
				return nil, err
			}
		}
	}
	var child *Tree
	var err error
	if binding.CurrentOf != "" {
		child = &Tree{label: "Cursor Locator on " + r.objectName(binding.Relation.Namespace(), binding.Relation.Name()), nodeType: "Cursor Locator", estimate: plan.ExplainEstimate{Rows: 1, Cost: 1}}
		r.relationProperties(child, binding.Relation, target.RangeName)
		if err = r.sourceDetails(child, target, binding.Relation); err != nil {
			return nil, err
		}
		if child.restriction != nil {
			child.estimate, err = plan.EstimateExplainNode(&plan.FilterNode{Predicate: child.restriction}, []plan.ExplainEstimate{child.estimate}, 0)
		}
	} else {
		child, err = r.node(source, synthetic...)
	}
	if err != nil {
		return nil, err
	}
	tree := &Tree{label: label + " on " + r.objectName(binding.Relation.Namespace(), binding.Relation.Name()), children: []*Tree{child}, estimate: plan.ExplainMutationEstimate(child.estimate, binding.Returning != nil)}
	tree.properties = modificationTree.properties
	if r.options.Debug {
		tree.commandDebug = map[string]any{"Relation OID": uint32(binding.Ref.Address.ObjectID), "Returning Target List": []string{}}
	}
	switch syntax := statement.(type) {
	case *parser.Insert:
		tree.nodeType = "Insert"
		if syntax.DefaultValues {
			r.property(tree, "Default Values", true)
		}
	case *parser.Update:
		tree.nodeType = "Update"
	case *parser.Delete:
		tree.nodeType = "Delete"
	case *parser.Merge:
		tree.nodeType = "Merge"
	}
	r.relationProperties(tree, binding.Relation, target.RangeName)
	tree.details = append(tree.details, modificationTree.details...)
	if original != binding.Relation {
		tree.details = append(tree.details, "Writable View: "+r.objectName(original.Namespace(), original.Name()))
		if r.options.Structured() {
			writable := &Tree{}
			r.relationProperties(writable, original, original.Name())
			r.property(tree, "Writable View", writable.properties)
		}
	}
	if r.options.Costs {
		tree.details = append(tree.details, fmt.Sprintf("Estimated candidates: %.0f", child.estimate.RoundedRows()))
		r.property(tree, "Estimated Candidates", child.estimate.RoundedRows())
	}
	if binding.CurrentOf != "" {
		tree.details = append(tree.details, "CURRENT OF: "+catalog.Identifier(binding.CurrentOf))
		r.property(tree, "Current Of", binding.CurrentOf)
	}
	if len(binding.Targets) != 0 {
		var targets []string
		var rawTargets []string
		for _, index := range binding.Targets {
			targets = append(targets, catalog.Identifier(binding.Relation.Columns[index].Name))
			if r.options.Structured() {
				rawTargets = append(rawTargets, binding.Relation.Columns[index].Name)
			}
		}
		tree.details = append(tree.details, "Target Columns: "+strings.Join(targets, ", "))
		r.property(tree, "Target Columns", rawTargets)
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
		if r.options.Structured() {
			column := fmt.Sprintf("column %d", i+1)
			if i < len(binding.Relation.Columns) {
				column = binding.Relation.Columns[i].Name
			}
			r.repeated(tree, "Defaults", map[string]any{"Column": column, "Expression": text})
		}
	}
	if binding.Update != nil {
		if err := r.assignments(tree, source, binding.Relation, binding.Targets, binding.Update); err != nil {
			return nil, err
		}
	}
	if conflict := binding.Insert.Conflict; conflict != nil {
		tree.details = append(tree.details, "On Conflict: DO NOTHING")
		r.property(tree, "Conflict Action", "DO NOTHING")
		if conflict.Update != nil {
			tree.details[len(tree.details)-1] = "On Conflict: DO UPDATE"
			r.property(tree, "Conflict Action", "DO UPDATE")
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
			r.repeated(tree, "Conflict Arbiter", text)
		}
		if conflict.Constraint.ObjectID != 0 {
			// Relation-local constraints are not catalog Descriptor entries.
			// The binder resolved this raw name to Constraint before rendering.
			syntax, ok := statement.(*parser.Insert)
			if !ok || syntax.Conflict == nil || syntax.Conflict.Constraint.Text == "" {
				return nil, fmt.Errorf("stuntgres: missing EXPLAIN conflict constraint syntax")
			}
			name := syntax.Conflict.Constraint.Text
			tree.details = append(tree.details, "Conflict Constraint: "+catalog.Identifier(name))
			r.property(tree, "Conflict Constraint", name)
		}
		if conflict.Predicate != nil {
			text, err := r.expression(target, conflict.Predicate)
			if err != nil {
				return nil, err
			}
			tree.details = append(tree.details, "Conflict Predicate: "+text)
			r.property(tree, "Conflict Predicate", text)
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
			armTree := tree
			if r.options.Structured() {
				armTree = &Tree{}
			}
			r.property(armTree, "Match", match)
			if arm.Condition != nil {
				condition, err := r.expression(source, arm.Condition)
				if err != nil {
					return nil, err
				}
				match += " AND " + condition
				r.property(armTree, "Condition", condition)
			}
			action := map[parser.MergeAction]string{parser.MergeInsertAction: "INSERT", parser.MergeUpdateAction: "UPDATE", parser.MergeDeleteAction: "DELETE", parser.MergeNothingAction: "DO NOTHING"}[arm.Action]
			tree.details = append(tree.details, "When "+match+" THEN "+action)
			r.property(armTree, "Action", action)
			if arm.Input.Update != nil {
				if err := r.assignments(armTree, source, binding.Relation, arm.Input.Targets, arm.Input.Update); err != nil {
					return nil, err
				}
				if armTree != tree {
					tree.details = append(tree.details, armTree.details...)
				}
			}
			for _, row := range arm.Input.Expressions {
				parts, err := r.valueParts(source, row)
				if err != nil {
					return nil, err
				}
				tree.details = append(tree.details, "Insert Values: "+strings.Join(parts, ", "))
				r.repeated(armTree, "Insert Values", parts)
			}
			if arm.Action == parser.MergeInsertAction || arm.Action == parser.MergeUpdateAction {
				command := catalog.PolicyInsert
				if arm.Action == parser.MergeUpdateAction {
					command = catalog.PolicyUpdate
				}
				before := len(armTree.details)
				if err := r.rowSecurityCheck(armTree, target, binding.Relation, command); err != nil {
					return nil, err
				}
				if armTree != tree {
					tree.details = append(tree.details, armTree.details[before:]...)
				}
			}
			if r.options.Structured() {
				columns := make([]string, len(arm.Input.Targets))
				for i, target := range arm.Input.Targets {
					columns[i] = binding.Relation.Columns[target].Name
				}
				r.property(armTree, "Target Columns", columns)
				r.repeated(tree, "Merge Arms", armTree.properties)
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
		parts, err := r.expressionParts(returningSource, binding.Returning.Expressions)
		if err != nil {
			return nil, err
		}
		tree.details = append(tree.details, "RETURNING: "+strings.Join(parts, ", "))
		r.property(tree, "Returning", parts)
		if r.options.Debug {
			tree.commandDebug["Returning Target List"] = parts
		}
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

func (r *Renderer) assignments(tree *Tree, source plan.Node, relation *catalog.Relation, targets []int, update *analyze.UpdateBinding) error {
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
			// Domain defaults are already bound assignment recipes, not nil
			// DEFAULT requests. Preserve their origin without deparsing twice.
			if r.options.Structured() && i < len(update.DefaultOrigins) && update.DefaultOrigins[i].Requested {
				r.repeated(tree, "Defaults", map[string]any{"Column": relation.Columns[targets[i]].Name, "Target": name, "Expression": text})
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
				if r.options.Structured() {
					r.repeated(tree, "Defaults", map[string]any{"Column": relation.Columns[targets[i]].Name, "Target": name, "Expression": defaultSQL})
				}
			}
		}
		tree.details = append(tree.details, "Assignment: "+name+" = "+text)
		if r.options.Structured() {
			r.repeated(tree, "Assignments", map[string]any{"Column": relation.Columns[targets[i]].Name, "Target": name, "Expression": text})
		}
	}
	if update.Predicate != nil {
		text, err := r.expression(source, update.Predicate)
		if err != nil {
			return err
		}
		tree.details = append(tree.details, "Action Filter: "+text)
		r.property(tree, "Action Filter", text)
	}
	return nil
}

func (r *Renderer) rowSecurityCheck(tree *Tree, target plan.Node, relation *catalog.Relation, command catalog.PolicyCommand) error {
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
		r.repeated(tree, "Row Security Checks", text)
	}
	return err
}

func (r *Renderer) assignmentTarget(source plan.Node, name string, target *plan.ArrayAssignmentTarget) (string, error) {
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
