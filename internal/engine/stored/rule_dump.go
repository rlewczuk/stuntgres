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
	"stuntgres/internal/engine/typedef"
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

func ruleExpressionSQL(ctx context.Context, view catalog.Read, bound expr.Expr, action RuleAction, sourceRoots ...plan.Node) (string, error) {
	r := Deparser{View: view, ShowImplicit: true}
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
		name, err := DeparseAttribute(d, column.Origin.Address.SubID)
		if err != nil {
			return "", err
		}
		if conflict := action.binding.Insert.Conflict; conflict != nil && conflict.Update != nil &&
			column.Scope == 1 && column.Frame == 1 && column.Slot >= conflict.Width && column.Slot < 2*conflict.Width &&
			a == action.binding.Ref.Address {
			return "EXCLUDED." + name, nil
		}
		if column.Scope == analyze.RulePseudoScope {
			side := "OLD"
			if column.Frame == 2 {
				side = "NEW"
			}
			return side + "." + name, nil
		}
		if column.QueryOccurrence > 0 {
			return catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(column.QueryOccurrence-1)) + "." + name, nil
		}
		return catalog.QuoteIdentifier(d.Name()) + "." + name, nil
	}
	r.NodeSQL = func(expr expr.Expr) (string, bool, error) {
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
		renderer := NewQueryRenderer(action.presentation)
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
		if subquery.Kind == parser.SubqueryExists {
			return "EXISTS (" + text + ")", true, nil
		}
		if subquery.Kind == parser.SubqueryScalar || subquery.Kind == parser.SubqueryRow {
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
			left = "ROW(" + left + ")"
		}
		quantifier := "ANY"
		if subquery.Kind == parser.SubqueryAll {
			quantifier = "ALL"
		}
		text = "(" + left + " " + subquery.Operator + " " + quantifier + " (" + text + "))"
		if subquery.Negate {
			text = "NOT " + text
		}
		return text, true, nil
	}
	node, _, err := RewriteViewExpression(view, bound, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
		if c, ok := expr.(plan.ReturningColumnExpr); ok {
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
func seedRuleMutationSlots(renderer *QueryRenderer, action RuleAction, query plan.Node, columnSQL func(plan.ColumnExpr) (string, error)) error {
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

func ruleFromSQL(ctx context.Context, view catalog.Read, root plan.Node, action RuleAction) (string, error) {
	if root == nil {
		return "", nil
	}
	switch n := root.(type) {
	case *plan.SourceNode:
		name, err := ruleRelationSQL(view, n.Ref)
		if err != nil {
			return "", err
		}
		return name + " AS " + catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(n.Occurrence)), nil
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
			return catalog.QuoteIdentifier(n.CTE.Name) + " AS " + catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(n.Ordinal)), nil
		}
		project, ok := n.Query.Root.(*plan.ProjectNode)
		if !ok || project.Syntax == nil || action.presentation == nil {
			return "", fmt.Errorf("stuntgres: missing rule derived-query presentation")
		}
		renderer := NewQueryRenderer(action.presentation)
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
		return "(" + text + ") AS " + catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(n.Ordinal)), nil
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
			names[i] = catalog.QuoteIdentifier(column.Name)
		}
		return text + " AS " + catalog.QuoteIdentifier("_rule_function_"+strconv.Itoa(n.ViewOrdinal)) + "(" + strings.Join(names, ", ") + ")", nil
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
			names[i] = catalog.QuoteIdentifier(column.Name)
		}
		return "(VALUES " + strings.Join(rows, ", ") + ") AS " + catalog.QuoteIdentifier("_rule_values") + "(" + strings.Join(names, ", ") + ")", nil
	case *plan.SingletonNode:
		return "", nil
	}
	return "", fmt.Errorf("stuntgres: missing rule FROM presentation for %T", root)
}

func RuleActionSQL(ctx context.Context, view catalog.Read, action RuleAction) (string, error) {
	b := action.binding
	if _, selecting := action.syntax.(*parser.Select); selecting {
		return action.presentation.SelectSQL(ctx, view, Presentation{})
	}
	name, err := ruleRelationSQL(view, b.Ref)
	if err != nil {
		return "", err
	}
	d := view.MustRelation(b.Ref.Address)
	var sql strings.Builder
	if definitions := parser.StatementWith(action.syntax); len(definitions) != 0 {
		renderer := NewQueryRenderer(action.presentation)
		prefix, err := renderer.WithClauseSQL(ctx, view, definitions, parser.StatementWithRecursive(action.syntax), func(statement parser.Statement) (string, error) {
			if query, ok := statement.(*parser.Select); ok {
				project := renderer.Queries()[query]
				if project == nil {
					return "", fmt.Errorf("stuntgres: missing retained rule WITH body")
				}
				return renderer.SelectQuerySQL(ctx, view, query, project)
			}
			for _, definition := range b.CTEs {
				mutation, ok := analyze.MutationBinding(definition)
				if definition.Statement != statement || !ok {
					continue
				}
				presentation := action.modifierPresentations[statement]
				if presentation == nil {
					return "", fmt.Errorf("stuntgres: missing retained rule modifier presentation")
				}
				return RuleActionSQL(ctx, view, RuleAction{syntax: statement, binding: *mutation, presentation: presentation, modifierPresentations: action.modifierPresentations})
			}
			return "", fmt.Errorf("stuntgres: missing retained rule modifier binding")
		})
		if err != nil {
			return "", err
		}
		sql.WriteString(prefix)
	}
	switch s := action.syntax.(type) {
	case *parser.Insert:
		sql.WriteString("INSERT INTO " + name)
		if len(b.Targets) != 0 {
			names := make([]string, len(b.Targets))
			for i, slot := range b.Targets {
				attribute := b.Relation.Attributes[slot]
				names[i], err = DeparseAttribute(d, attribute)
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
			text, err := action.presentation.SelectSQL(ctx, view, Presentation{})
			if err != nil {
				return "", err
			}
			sql.WriteString(" " + text)
		} else {
			sql.WriteString(" VALUES ")
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
				sql.WriteString("(" + strings.Join(parts, ", ") + ")")
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
				sql.WriteString(" ON CONSTRAINT " + catalog.QuoteIdentifier(canonicalName))
			} else if len(b.Insert.Conflict.Keys) != 0 {
				parts := make([]string, len(b.Insert.Conflict.Keys))
				for i, key := range b.Insert.Conflict.Keys {
					if key.Attribute != 0 {
						parts[i], err = DeparseAttribute(d, key.Attribute)
					} else {
						parts[i], err = ruleExpressionSQL(ctx, view, key.Expression, action)
						parts[i] = "(" + parts[i] + ")"
					}
					if err != nil {
						return "", err
					}
					if key.Collation != 0 {
						parts[i] += " COLLATE " + typedef.CollationSQLName(view, key.Collation)
					}
					if key.Class.OID() != 0 {
						class, exists := view.OperatorClass(ids.OID(key.Class.OID()))
						if !exists {
							return "", fmt.Errorf("stuntgres: missing conflict operator class")
						}
						namespace, exists := view.Namespace(class.Namespace())
						if !exists {
							return "", fmt.Errorf("stuntgres: missing conflict operator class namespace")
						}
						parts[i] += " " + catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(class.Name())
					}
				}
				sql.WriteString(" (" + strings.Join(parts, ", ") + ")")
			}
			if b.Insert.Conflict.Predicate != nil {
				text, err := ruleExpressionSQL(ctx, view, b.Insert.Conflict.Predicate, action)
				if err != nil {
					return "", err
				}
				sql.WriteString(" WHERE " + text)
			}
			if b.Insert.Conflict.Update == nil {
				sql.WriteString(" DO NOTHING")
			} else {
				sql.WriteString(" DO UPDATE SET ")
				for i, expr := range b.Insert.Conflict.Update.Assignments {
					if i != 0 {
						sql.WriteString(", ")
					}
					column, err := DeparseAttribute(d, b.Relation.Attributes[b.Insert.Conflict.UpdateTargets[i]])
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
					sql.WriteString(" WHERE " + text)
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
			sql.WriteString(" FROM " + text)
		}
		if b.Update.Predicate != nil {
			text, err := ruleExpressionSQL(ctx, view, b.Update.Predicate, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(" WHERE " + text)
		}
	case *parser.Delete:
		sql.WriteString("DELETE FROM " + name)
		if b.Delete.Using != nil {
			text, err := ruleFromSQL(ctx, view, b.Delete.Using, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(" USING " + text)
		}
		if b.Predicate != nil {
			text, err := ruleExpressionSQL(ctx, view, b.Predicate, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(" WHERE " + text)
		}
	case *parser.Merge:
		text, err := mergeActionSQL(ctx, view, action)
		if err != nil {
			return "", err
		}
		sql.WriteString(text)
	}
	if b.Returning != nil {
		parts := make([]string, len(b.Returning.Expressions))
		for i, expr := range b.Returning.Expressions {
			parts[i], err = ruleExpressionSQL(ctx, view, expr, action)
			if err != nil {
				return "", err
			}
		}
		sql.WriteString(" RETURNING " + strings.Join(parts, ", "))
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
			groups = append(groups, dump.Group{Owner: owner.Name, Table: table, SQL: sql.String(), Binding: binding})
		}
	}
	slices.SortStableFunc(groups, func(a, b dump.Group) int { return strings.Compare(a.Table, b.Table) })
	return groups, nil
}
