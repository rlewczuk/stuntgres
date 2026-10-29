package stored

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func ruleRelationSQL(view catalog.Read, ref ids.ObjectRef) (string, error) {
	d, ok := view.Relation(ref.Address)
	if !ok {
		return "", ids.ErrCatalogReplaced
	}
	ns, ok := view.Namespace(d.Namespace())
	if !ok {
		return "", ids.ErrCatalogReplaced
	}
	return catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(d.Name()), nil
}

func ruleExpressionSQL(ctx context.Context, view catalog.Read, bound expr.Expr, action ruleRendering, sourceRoots ...plan.Node) (string, error) {
	r := Deparser{View: view, Pretty: action.policy.Pretty, ShowImplicit: true, Path: action.policy.Path, Output: action.policy.Output, NonstandardStrings: action.policy.NonstandardStrings}
	r.ColumnSQL = func(column plan.ColumnExpr) (string, error) {
		if column.Scope == 1 && column.Frame == 1 && len(sourceRoots) != 0 {
			return ruleSourceColumnSQL(view, sourceRoots[0], column.Slot, action, func(root plan.Node, expr expr.Expr) (string, error) {
				return ruleExpressionSQL(ctx, view, expr, action, root)
			})
		}
		if column.Scope != analyze.RulePseudoScope && column.Scope == 1 && column.Frame == 1 && action.binding.Relation != nil {
			offset := len(action.binding.Relation.Columns)
			var from plan.Node
			if action.binding.Update != nil {
				from = action.binding.Update.From
			} else if action.binding.Merge != nil {
				from = action.binding.Merge.Source
			} else {
				from = action.binding.Delete.Using
			}
			if from != nil && column.Slot >= offset {
				return ruleSourceColumnSQL(view, from, column.Slot-offset, action, func(root plan.Node, expr expr.Expr) (string, error) {
					return ruleExpressionSQL(ctx, view, expr, action, root)
				})
			}
		}
		a := column.Origin.Address
		a.SubID = 0
		d, ok := view.Relation(a)
		if !ok {
			return "", ids.ErrCatalogReplaced
		}
		name, err := action.attribute(d, column.Origin.Address.SubID)
		if err != nil {
			return "", err
		}
		if action.inference && a == action.binding.Ref.Address && column.Scope != analyze.RulePseudoScope {
			return name, nil
		}
		if conflict := action.binding.Insert.Conflict; conflict != nil && conflict.Update != nil &&
			column.Scope == 1 && column.Frame == 1 && column.Slot >= conflict.Width && column.Slot < 2*conflict.Width &&
			a == action.binding.Ref.Address {
			qualifier := "EXCLUDED"
			if action.policy.Rule {
				qualifier = "excluded"
				if action.names != nil {
					qualifier = action.names.excluded
				}
				qualifier = action.policy.identifier(qualifier)
			}
			return qualifier + "." + name, nil
		}
		if column.Scope == analyze.RulePseudoScope {
			side := "OLD"
			if column.Frame == 2 {
				side = "NEW"
			}
			if action.policy.Deparse {
				side = "old"
				if column.Frame == 2 {
					side = "new"
				}
				if action.policy.Rule {
					side = action.names.pseudoName(column.Frame == 1)
				}
				side = action.policy.identifier(side)
			}
			return side + "." + name, nil
		}
		if action.returning && a == action.binding.Ref.Address {
			if qualifier := action.returningNames[column.SourcePosition()]; qualifier != "" {
				return action.policy.identifier(qualifier) + "." + name, nil
			}
		}
		if action.policy.Rule && a == action.binding.Ref.Address {
			return action.targetQualifier(d) + "." + name, nil
		}
		if column.QueryOccurrence > 0 {
			return action.policy.identifier("_rule_source_"+strconv.Itoa(column.QueryOccurrence-1)) + "." + name, nil
		}
		return action.policy.identifier(d.Name()) + "." + name, nil
	}
	r.NodeSQL = func(expr expr.Expr) (string, bool, error) {
		if system, ok := expr.(plan.TableOIDExpr); ok && system.Scope != 0 {
			if action.presentation == nil {
				return "", true, fmt.Errorf("stuntgres: missing tableoid source presentation")
			}
			if _, err := action.presentation.tableOIDSource(view, analyze.ViewColumn{TableOID: true, Owner: system.Owner, Scope: system.Scope, Frame: system.Frame, Occurrence: system.QueryOccurrence}); err != nil {
				return "", true, err
			}
			qualifier := action.sourceQualifiers[system.QueryOccurrence-1]
			if !action.policy.Rule {
				qualifier = action.policy.identifier("_rule_source_" + strconv.Itoa(system.QueryOccurrence-1))
			}
			if qualifier == "" {
				return "", true, fmt.Errorf("stuntgres: missing rule tableoid qualifier")
			}
			return qualifier + ".tableoid", true, nil
		}
		if column, ok := expr.(plan.ReturningColumnExpr); ok && action.returning {
			relation, exists := view.Relation(action.binding.Ref.Address)
			if !exists {
				return "", true, ids.ErrCatalogReplaced
			}
			name, err := action.attribute(relation, column.Origin.Address.SubID)
			return action.policy.identifier(action.returningAlias(column.Old)) + "." + name, true, err
		}
		subquery, ok := expr.(plan.SubqueryExpr)
		if !ok {
			return "", false, nil
		}
		if action.presentation == nil {
			return "", true, fmt.Errorf("stuntgres: missing rule subquery presentation")
		}
		project, ok := subquery.QueryPlan.Root.(*plan.ProjectNode)
		if !ok || project.Syntax == nil {
			return "", true, fmt.Errorf("stuntgres: missing rule subquery syntax")
		}
		renderer := action.renderer(action.presentation)
		renderer.Presentation = action.policy.nestedQueryPresentation(false)
		// Replacement remaps executable and frozen graphs independently.
		// Every slot cache and frame lookup in this render must use the
		// retained presentation graph, selected by immutable syntax identity.
		project = renderer.Queries()[project.Syntax]
		if project == nil {
			return "", true, fmt.Errorf("stuntgres: missing retained rule subquery presentation")
		}
		if err := seedRuleMutationSlots(renderer, action, project, r.ColumnSQL); err != nil {
			return "", true, err
		}
		text, err := renderer.SelectQuerySQL(ctx, view, project.Syntax, project)
		if err != nil {
			return "", true, err
		}
		if subquery.Kind == parser.SubqueryExists || subquery.Kind == parser.SubqueryScalar || subquery.Kind == parser.SubqueryRow && len(subquery.Comparisons) == 0 {
			if action.policy.Rule {
				return ruleSublinkSQL(subquery.Kind, "", "", text, subquery.Negate), true, nil
			}
			if subquery.Kind == parser.SubqueryExists {
				return "EXISTS (" + text + ")", true, nil
			}
			return "(" + text + ")", true, nil
		}
		parts := make([]string, len(subquery.Comparisons))
		for i, comparison := range subquery.Comparisons {
			parts[i], err = r.SQL(ctx, comparison.Left, DeparseParent{})
			if err != nil {
				return "", true, err
			}
		}
		left := strings.Join(parts, ", ")
		if len(parts) > 1 {
			prefix := "ROW"
			if action.policy.Rule {
				prefix = ""
			}
			left = prefix + "(" + left + ")"
		}
		operator := subquery.Operator
		if action.policy.Rule {
			return ruleSublinkSQL(subquery.Kind, operator, left, text, subquery.Negate), true, nil
		}
		quantifier := "ANY"
		switch subquery.Kind {
		case parser.SubqueryAll:
			quantifier = "ALL"
		case parser.SubqueryRow:
			quantifier = ""
		}
		if quantifier != "" {
			operator += " " + quantifier
		}
		text = "(" + left + " " + operator + " (" + text + "))"
		if subquery.Negate {
			text = "NOT " + text
		}
		return text, true, nil
	}
	node, _, err := RewriteViewExpression(view, bound, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
		if c, ok := expr.(plan.ReturningColumnExpr); ok && !action.returning {
			return c.ColumnExpr, true, nil
		}
		return expr, false, nil
	})
	if err != nil {
		return "", err
	}
	return r.SQL(ctx, node, DeparseParent{})
}

// A mutation's target/FROM frame is not emitted by SELECT rendering. Seed only
// actual correlated recipes under that frozen outer root, using the same target
// and source-offset authority as ordinary rule expressions. Unreferenced retired
// columns are never rendered, and the frozen presentation remains immutable.
func seedRuleMutationSlots(renderer *QueryRenderer, action ruleRendering, query plan.Node, columnSQL func(plan.ColumnExpr) (string, error)) error {
	switch action.syntax.(type) {
	case *parser.Update, *parser.Delete:
	default:
		return nil
	}
	owner := renderer.Frames()[1]
	if owner == nil {
		return fmt.Errorf("stuntgres: missing rule mutation presentation frame")
	}
	root := analyze.ViewFromRoot(owner)
	slots := renderer.Slots[root]
	var failure error
	plan.VisitCTEQuery(query, func(plan.Node) {}, func(bound expr.Expr) {
		plan.WalkExpression(bound, func(node expr.Expr) {
			column, ok := node.(plan.ColumnExpr)
			if failure != nil || !ok || column.Scope != 1 || column.Frame != 1 {
				return
			}
			if _, exists := slots[column.Slot]; exists {
				return
			}
			text, err := columnSQL(column)
			if err != nil {
				failure = err
				return
			}
			if slots == nil {
				slots = make(map[int]string)
				renderer.Slots[root] = slots
			}
			slots[column.Slot] = text
		})
	}, true)
	return failure
}

func ruleFromSQL(ctx context.Context, view catalog.Read, root plan.Node, action ruleRendering) (string, error) {
	if root == nil {
		return "", nil
	}
	if action.policy.Rule && root == action.sourceRoot {
		return action.sourceText, nil
	}
	switch n := root.(type) {
	case *plan.SourceNode:
		name, err := action.relation(view, n.Ref, false)
		if err != nil {
			return "", err
		}
		return name + " AS " + action.policy.identifier("_rule_source_"+strconv.Itoa(n.Occurrence)), nil
	case *plan.JoinNode:
		left, err := ruleFromSQL(ctx, view, n.Left, action)
		if err != nil {
			return "", err
		}
		right, err := ruleFromSQL(ctx, view, n.Right, action)
		if err != nil {
			return "", err
		}
		if n.Predicate == nil {
			return "(" + left + " CROSS JOIN " + right + ")", nil
		}
		predicate, err := ruleExpressionSQL(ctx, view, n.Predicate, action, n)
		if err != nil {
			return "", err
		}
		kind := "INNER"
		switch n.Kind {
		case parser.JoinLeft:
			kind = "LEFT"
		case parser.JoinRight:
			kind = "RIGHT"
		case parser.JoinFull:
			kind = "FULL"
		}
		return "(" + left + " " + kind + " JOIN " + right + " ON " + predicate + ")", nil
	case *plan.DerivedNode:
		if n.CTE != nil {
			return action.policy.identifier(n.CTE.Name) + " AS " + action.policy.identifier("_rule_source_"+strconv.Itoa(n.Ordinal)), nil
		}
		project, ok := n.Query.Root.(*plan.ProjectNode)
		if !ok || project.Syntax == nil || action.presentation == nil {
			return "", fmt.Errorf("stuntgres: missing rule derived-query presentation")
		}
		renderer := action.renderer(action.presentation)
		project = renderer.Queries()[project.Syntax]
		if project == nil {
			return "", fmt.Errorf("stuntgres: missing retained rule derived-query presentation")
		}
		if err := seedRuleMutationSlots(renderer, action, project, func(column plan.ColumnExpr) (string, error) {
			return ruleExpressionSQL(ctx, view, column, action)
		}); err != nil {
			return "", err
		}
		text, err := renderer.SelectQuerySQL(ctx, view, project.Syntax, project)
		if err != nil {
			return "", err
		}
		return "(" + text + ") AS " + action.policy.identifier("_rule_source_"+strconv.Itoa(n.Ordinal)), nil
	case *plan.FunctionFromNode:
		parts := make([]string, len(n.Calls))
		for i, call := range n.Calls {
			var err error
			parts[i], err = ruleExpressionSQL(ctx, view, call, action)
			if err != nil {
				return "", err
			}
		}
		text := "ROWS FROM (" + strings.Join(parts, ", ") + ")"
		if n.Ordinality {
			text += " WITH ORDINALITY"
		}
		if n.Correlated {
			text = "LATERAL " + text
		}
		names := make([]string, len(n.Schema()))
		for i, column := range n.Schema() {
			names[i] = action.policy.identifier(column.Name)
		}
		return text + " AS " + action.policy.identifier("_rule_function_"+strconv.Itoa(n.ViewOrdinal)) + "(" + strings.Join(names, ", ") + ")", nil
	case *plan.ProjectNode, *plan.SortNode, *plan.LimitNode, *plan.EvaluationNode:
		return ruleValuesFromSQL(ctx, view, root, action)
	case *plan.ValuesNode:
		var rows []string
		for _, row := range n.Rows {
			parts := make([]string, len(row))
			for i, expr := range row {
				var err error
				parts[i], err = ruleExpressionSQL(ctx, view, expr, action)
				if err != nil {
					return "", err
				}
			}
			rows = append(rows, "("+strings.Join(parts, ", ")+")")
		}
		names := make([]string, len(n.Schema()))
		for i, column := range n.Schema() {
			names[i] = action.policy.identifier(column.Name)
		}
		return "(VALUES " + strings.Join(rows, ", ") + ") AS " + action.policy.identifier("_rule_values") + "(" + strings.Join(names, ", ") + ")", nil
	case *plan.SingletonNode:
		return "", nil
	}
	return "", fmt.Errorf("stuntgres: missing rule FROM presentation for %T", root)
}

func RuleActionSQL(ctx context.Context, view catalog.Read, action RuleAction) (string, error) {
	return ruleActionSQL(ctx, view, ruleRendering{RuleAction: action})
}

func ruleActionSQL(ctx context.Context, view catalog.Read, action ruleRendering) (string, error) {
	b := action.binding
	if _, selecting := action.syntax.(*parser.Select); selecting {
		renderer := action.renderer(action.presentation)
		return renderer.SelectQuerySQL(ctx, view, action.presentation.syntax, action.presentation.queries[action.presentation.syntax])
	}
	if _, calling := action.syntax.(*parser.Call); calling {
		if len(b.Expressions) != 1 || len(b.Expressions[0]) != 1 {
			return "", fmt.Errorf("stuntgres: missing CALL capture")
		}
		text, err := ruleExpressionSQL(ctx, view, b.Expressions[0][0], action)
		if err != nil {
			return "", err
		}
		return "CALL " + text, nil
	}
	name, err := action.relation(view, b.Ref, false)
	if err != nil {
		return "", err
	}
	d := view.MustRelation(b.Ref.Address)
	if err := action.prepareNamespaces(ctx, view); err != nil {
		return "", err
	}
	name = action.targetDeclaration(name)
	var sql strings.Builder
	if definitions := parser.StatementWith(action.syntax); len(definitions) != 0 {
		renderer := action.renderer(action.presentation)
		prefix, err := renderer.WithClauseSQL(ctx, view, definitions, parser.StatementWithRecursive(action.syntax), func(statement parser.Statement) (string, error) {
			if query, ok := statement.(*parser.Select); ok {
				project := renderer.Queries()[query]
				if project == nil {
					return "", fmt.Errorf("stuntgres: missing retained rule WITH body")
				}
				return renderer.SelectQuerySQL(ctx, view, query, project)
			}
			return renderer.ruleModifierSQL(ctx, view, statement, renderer.ruleNameFacts(true))
		})
		if err != nil {
			return "", err
		}
		sql.WriteString(prefix)
	}
	if action.policy.Deparse {
		sql.WriteByte(' ')
	}
	switch s := action.syntax.(type) {
	case *parser.Insert:
		sql.WriteString("INSERT INTO " + name)
		if len(b.Targets) != 0 {
			names := make([]string, len(b.Targets))
			for i, slot := range b.Targets {
				attribute := b.Relation.Attributes[slot]
				names[i], err = action.attribute(d, attribute)
				if err != nil {
					return "", err
				}
				if len(b.ArrayTargets) > i {
					names[i], err = ruleAssignmentSQL(ctx, view, names[i], b.ArrayTargets[i], action)
					if err != nil {
						return "", err
					}
				}
			}
			sql.WriteString(" (" + strings.Join(names, ", ") + ")")
		}
		if s.Override == parser.OverrideSystem {
			sql.WriteString(" OVERRIDING SYSTEM VALUE")
		} else if s.Override == parser.OverrideUser {
			sql.WriteString(" OVERRIDING USER VALUE")
		}
		if s.DefaultValues {
			sql.WriteString(" DEFAULT VALUES")
		} else if action.presentation != nil && s.Source != nil && b.Query != nil {
			policy := action.policy
			if policy.Rule {
				policy.Indent += 8
			}
			source := action
			source.policy = policy
			renderer := source.renderer(action.presentation)
			text, err := renderer.SelectQuerySQL(ctx, view, action.presentation.syntax, action.presentation.queries[action.presentation.syntax])
			if err != nil {
				return "", err
			}
			sql.WriteString(" " + text)
		} else {
			multiple := action.policy.Rule && len(b.Expressions) > 1
			if multiple {
				sql.WriteString(" VALUES ")
			} else {
				sql.WriteString(action.clause(" VALUES ", 2, "VALUES "))
			}
			for i, row := range b.Expressions {
				if i != 0 {
					sql.WriteString(", ")
				}
				parts := make([]string, len(row))
				for j, expr := range row {
					parts[j], err = ruleExpressionSQL(ctx, view, expr, action)
					if err != nil {
						return "", err
					}
				}
				separator := ", "
				if multiple {
					separator = ","
				}
				sql.WriteString("(" + strings.Join(parts, separator) + ")")
			}
		}
		if s.Conflict != nil {
			sql.WriteString(" ON CONFLICT")
			if constraint := b.Insert.Conflict.Constraint; constraint != (ids.ObjectAddress{}) {
				if constraint.ClassID != 2606 || constraint.SubID != 0 {
					return "", fmt.Errorf("stuntgres: invalid named rule constraint identity")
				}
				// Names belong to the current parent metadata, not the frozen
				// action's creation spelling or a newly reused name.
				canonicalName := ConstraintName(d, constraint)
				if canonicalName == "" {
					return "", fmt.Errorf("stuntgres: missing named rule constraint")
				}
				sql.WriteString(" ON CONSTRAINT " + action.policy.identifier(canonicalName))
			} else {
				text, err := action.conflictInferenceSQL(ctx, view, d)
				if err != nil {
					return "", err
				}
				sql.WriteString(text)
			}
			if b.Insert.Conflict.Update == nil {
				sql.WriteString(" DO NOTHING")
			} else {
				sql.WriteString(" DO UPDATE SET ")
				for i, expr := range b.Insert.Conflict.Update.Assignments {
					if i != 0 {
						sql.WriteString(", ")
					}
					column, err := action.attribute(d, b.Relation.Attributes[b.Insert.Conflict.UpdateTargets[i]])
					if err != nil {
						return "", err
					}
					if len(b.Insert.Conflict.Update.ArrayTargets) > i {
						column, err = ruleAssignmentSQL(ctx, view, column, b.Insert.Conflict.Update.ArrayTargets[i], action)
						if err != nil {
							return "", err
						}
					}
					text, err := ruleExpressionSQL(ctx, view, expr, action)
					if err != nil {
						return "", err
					}
					sql.WriteString(column + " = " + text)
				}
				if b.Insert.Conflict.Update.Predicate != nil {
					text, err := ruleExpressionSQL(ctx, view, b.Insert.Conflict.Update.Predicate, action)
					if err != nil {
						return "", err
					}
					sql.WriteString(action.clause(" WHERE ", 2, "WHERE ") + text)
				}
			}
		}
	case *parser.Update:
		sql.WriteString("UPDATE " + name + " SET ")
		assignments, err := ruleUpdateAssignmentsSQL(ctx, view, &b, action)
		if err != nil {
			return "", err
		}
		sql.WriteString(assignments)
		if b.Update.From != nil {
			text, err := ruleFromSQL(ctx, view, b.Update.From, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(action.clause(" FROM ", 3, "FROM ") + text)
		}
		if b.Update.Predicate != nil {
			text, err := ruleExpressionSQL(ctx, view, b.Update.Predicate, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(action.clause(" WHERE ", 2, "WHERE ") + text)
		}
		if s.CurrentOf != nil {
			sql.WriteString(action.clause(" WHERE ", 2, "WHERE ") + "CURRENT OF " + action.policy.identifier(s.CurrentOf.Text))
		}
	case *parser.Delete:
		sql.WriteString("DELETE FROM " + name)
		if b.Delete.Using != nil {
			text, err := ruleFromSQL(ctx, view, b.Delete.Using, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(action.clause(" USING ", 3, "USING ") + text)
		}
		if b.Predicate != nil {
			text, err := ruleExpressionSQL(ctx, view, b.Predicate, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(action.clause(" WHERE ", 2, "WHERE ") + text)
		}
		if s.CurrentOf != nil {
			sql.WriteString(action.clause(" WHERE ", 2, "WHERE ") + "CURRENT OF " + action.policy.identifier(s.CurrentOf.Text))
		}
	case *parser.Merge:
		text, err := mergeActionSQL(ctx, view, action)
		if err != nil {
			return "", err
		}
		sql.WriteString(text)
	}
	if b.Returning != nil {
		options, err := action.prepareReturning()
		if err != nil {
			return "", err
		}
		parts := make([]string, len(b.Returning.Expressions))
		for i, expr := range b.Returning.Expressions {
			parts[i], err = ruleExpressionSQL(ctx, view, expr, action)
			if err != nil {
				return "", err
			}
			if action.policy.Rule {
				natural, err := ruleReturningNatural(view, expr, action)
				if err != nil {
					return "", err
				}
				label := action.policy.identifier(b.Returning.Schema[i].Name)
				if natural != label {
					parts[i] += " AS " + label
				}
			}
		}
		separator := ", "
		if action.policy.Rule {
			separator = "," + action.policy.line(4)
		}
		sql.WriteString(action.clause(" RETURNING ", 2, "RETURNING ") + options + strings.Join(parts, separator))
	}
	return sql.String(), nil
}

func RuleDumpGroups(ctx context.Context, view catalog.Read, roles authorization.Read) ([]dump.Group, error) {
	var groups []dump.Group
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(RulesOf(d)) == 0 {
			continue
		}
		ns := view.MustNamespace(d.Namespace())
		if ns.Temporary != (ids.SessionToken{}) {
			continue
		}
		owner, err := roles.Descriptor(d.Owner)
		if err != nil {
			return nil, err
		}
		table, err := ruleRelationSQL(view, ids.ObjectRef{Address: d.Address()})
		if err != nil {
			return nil, err
		}
		var sql strings.Builder
		for _, r := range RulesOf(d) {
			sql.Reset()
			var binding *dump.DefinitionBinding
			var bindingFailure error
			visit := func(node expr.Expr) {
				if bindingFailure != nil {
					return
				}
				namespace, failure := ComparisonBindingNamespace(view, node)
				if failure != nil {
					bindingFailure = failure
					return
				}
				if namespace != (ids.ObjectAddress{}) {
					if binding != nil && binding.CubeNamespace != namespace {
						bindingFailure = fmt.Errorf("stuntgres: conflicting comparison binding namespaces")
						return
					}
					binding = &dump.DefinitionBinding{CubeNamespace: namespace}
				}
			}
			visit(r.condition)
			for _, action := range r.actions {
				VisitRuleActionExpressions(action.binding, visit)
				if action.binding.Query != nil {
					plan.VisitCTEQuery(action.binding.Query.Root, func(plan.Node) {}, visit, true)
				}
			}
			if bindingFailure != nil {
				return nil, bindingFailure
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, address := range r.references {
				address.SubID = 0
				var provider catalog.Descriptor
				if address.ClassID == 2606 {
					parent := ConstraintParent(view, address)
					if parent == nil {
						return nil, ids.ErrCatalogReplaced
					}
					provider = parent
				} else {
					provider = view.Get(address)
				}
				if provider == nil {
					return nil, ids.ErrCatalogReplaced
				}
				if namespace, ok := view.Namespace(provider.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
					return nil, fmt.Errorf("stuntgres: dump cannot serialize a rule referencing a temporary relation")
				}
			}
			sql.WriteString("CREATE RULE " + catalog.QuoteIdentifier(r.Name) + " AS ON " + r.event + " TO " + table)
			if r.condition != nil {
				renderer := NewQueryRenderer(r.conditionPresentation)
				renderer.Frame = r.conditionPresentation.Query().Root.(*plan.ProjectNode).Frame
				text, err := renderer.ExpressionSQL(ctx, view, r.conditionPresentation.Syntax().Targets[0], analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
				if err != nil {
					return nil, err
				}
				sql.WriteString(" WHERE (" + text + ")")
			}
			sql.WriteString(" DO ")
			if r.instead {
				sql.WriteString("INSTEAD ")
			} else {
				sql.WriteString("ALSO ")
			}
			if len(r.actions) == 0 {
				sql.WriteString("NOTHING")
			} else {
				parts := make([]string, len(r.actions))
				for i, action := range r.actions {
					parts[i], err = RuleActionSQL(ctx, view, action)
					if err != nil {
						return nil, err
					}
				}
				if len(parts) > 1 {
					sql.WriteString("(" + strings.Join(parts, "; ") + ")")
				} else {
					sql.WriteString(parts[0])
				}
			}
			sql.WriteString(";\n")
			switch r.mode {
			case catalog.RuleOrigin:
			case catalog.RuleDisabled:
				sql.WriteString("ALTER TABLE " + table + " DISABLE RULE " + catalog.QuoteIdentifier(r.Name) + ";\n")
			case catalog.RuleReplica:
				sql.WriteString("ALTER TABLE " + table + " ENABLE REPLICA RULE " + catalog.QuoteIdentifier(r.Name) + ";\n")
			case catalog.RuleAlways:
				sql.WriteString("ALTER TABLE " + table + " ENABLE ALWAYS RULE " + catalog.QuoteIdentifier(r.Name) + ";\n")
			default:
				return nil, fmt.Errorf("stuntgres: invalid rule enable mode")
			}
			groups = append(groups, dump.Group{Owner: owner.Name, Table: table, SQL: sql.String(), Binding: binding})
		}
	}
	slices.SortStableFunc(groups, func(a, b dump.Group) int { return strings.Compare(a.Table, b.Table) })
	return groups, nil
}
