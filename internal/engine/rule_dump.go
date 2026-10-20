package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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

func ruleExpressionSQL(ctx context.Context, view catalog.Read, bound expr.Expr, action ruleAction, sourceRoots ...LogicalNode) (string, error) {
	r := pgRenderer{view: view, showImplicit: true}
	r.columnSQL = func(column columnExpression) (string, error) {
		if column.Scope == 1 && column.Frame == 1 && len(sourceRoots) != 0 {
			return ruleSourceColumnSQL(view, sourceRoots[0], column.Slot, action, func(root LogicalNode, expr expr.Expr) (string, error) {
				return ruleExpressionSQL(ctx, view, expr, action, root)
			})
		}
		if column.Scope != rulePseudoScope && column.Scope == 1 && column.Frame == 1 && action.binding.relation != nil {
			offset := len(action.binding.relation.Columns)
			var from LogicalNode
			if action.binding.update != nil {
				from = action.binding.update.from
			} else {
				from = action.binding.delete.using
			}
			if from != nil && column.Slot >= offset {
				return ruleSourceColumnSQL(view, from, column.Slot-offset, action, func(root LogicalNode, expr expr.Expr) (string, error) {
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
		name, err := deparseAttribute(d, column.Origin.Address.SubID)
		if err != nil {
			return "", err
		}
		if conflict := action.binding.insert.conflict; conflict != nil && conflict.update != nil &&
			column.Scope == 1 && column.Frame == 1 && column.Slot >= conflict.width && column.Slot < 2*conflict.width &&
			a == action.binding.ref.Address {
			return "EXCLUDED." + name, nil
		}
		if column.Scope == rulePseudoScope {
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
	r.nodeSQL = func(expr expr.Expr) (string, bool, error) {
		subquery, ok := expr.(subqueryExpression)
		if !ok {
			return "", false, nil
		}
		if action.presentation == nil {
			return "", true, fmt.Errorf("stuntgres: missing rule subquery presentation")
		}
		project, ok := subquery.QueryPlan.Root.(*projectNode)
		if !ok || project.Syntax == nil {
			return "", true, fmt.Errorf("stuntgres: missing rule subquery syntax")
		}
		renderer := newViewRenderer(action.presentation)
		// Replacement remaps executable and frozen graphs independently.
		// Every slot cache and frame lookup in this render must use the
		// retained presentation graph, selected by immutable syntax identity.
		project = renderer.queries[project.Syntax]
		if project == nil {
			return "", true, fmt.Errorf("stuntgres: missing retained rule subquery presentation")
		}
		if err := seedRuleMutationSlots(renderer, action, project, r.columnSQL); err != nil {
			return "", true, err
		}
		text, err := renderer.selectQuerySQL(ctx, view, project.Syntax, project)
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
			parts[i], err = r.sql(ctx, comparison.Left, pgRenderParent{})
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
	node, _, err := rewriteViewExpression(view, bound, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
		if c, ok := expr.(returningColumnExpression); ok {
			return c.ColumnExpr, true, nil
		}
		return expr, false, nil
	})
	if err != nil {
		return "", err
	}
	return r.sql(ctx, node, pgRenderParent{})
}

// A mutation's target/FROM frame is not emitted by SELECT rendering. Seed only
// actual correlated recipes under that frozen outer root, using the same target
// and source-offset authority as ordinary rule expressions. Unreferenced retired
// columns are never rendered, and the frozen presentation remains immutable.
func seedRuleMutationSlots(renderer *viewRenderer, action ruleAction, query LogicalNode, columnSQL func(columnExpression) (string, error)) error {
	switch action.syntax.(type) {
	case *parser.Update, *parser.Delete:
	default:
		return nil
	}
	owner := renderer.frames[1]
	if owner == nil {
		return fmt.Errorf("stuntgres: missing rule mutation presentation frame")
	}
	root := viewFromRoot(owner)
	slots := renderer.slots[root]
	var failure error
	visitCTEQuery(query, func(LogicalNode) {}, func(bound expr.Expr) {
		walkBoundExpression(bound, func(node expr.Expr) {
			column, ok := node.(columnExpression)
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
				renderer.slots[root] = slots
			}
			slots[column.Slot] = text
		})
	}, true)
	return failure
}

func ruleFromSQL(ctx context.Context, view catalog.Read, root LogicalNode, action ruleAction) (string, error) {
	if root == nil {
		return "", nil
	}
	switch n := root.(type) {
	case *sourceNode:
		name, err := ruleRelationSQL(view, n.Ref)
		if err != nil {
			return "", err
		}
		return name + " AS " + catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(n.Occurrence)), nil
	case *joinNode:
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
	case *derivedNode:
		if n.CTE != nil {
			return catalog.QuoteIdentifier(n.CTE.Name) + " AS " + catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(n.Ordinal)), nil
		}
		project, ok := n.Query.Root.(*projectNode)
		if !ok || project.Syntax == nil || action.presentation == nil {
			return "", fmt.Errorf("stuntgres: missing rule derived-query presentation")
		}
		renderer := newViewRenderer(action.presentation)
		project = renderer.queries[project.Syntax]
		if project == nil {
			return "", fmt.Errorf("stuntgres: missing retained rule derived-query presentation")
		}
		if err := seedRuleMutationSlots(renderer, action, project, func(column columnExpression) (string, error) {
			return ruleExpressionSQL(ctx, view, column, action)
		}); err != nil {
			return "", err
		}
		text, err := renderer.selectQuerySQL(ctx, view, project.Syntax, project)
		if err != nil {
			return "", err
		}
		return "(" + text + ") AS " + catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(n.Ordinal)), nil
	case *functionFromNode:
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
	case *valuesNode:
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
	case *singletonNode:
		return "", nil
	}
	return "", fmt.Errorf("stuntgres: missing rule FROM presentation for %T", root)
}

func ruleActionSQL(ctx context.Context, view catalog.Read, action ruleAction) (string, error) {
	b := action.binding
	if _, selecting := action.syntax.(*parser.Select); selecting {
		return action.presentation.selectSQL(ctx, view, viewPresentation{})
	}
	name, err := ruleRelationSQL(view, b.ref)
	if err != nil {
		return "", err
	}
	d := view.MustRelation(b.ref.Address)
	var sql strings.Builder
	if definitions := parser.StatementWith(action.syntax); len(definitions) != 0 {
		renderer := newViewRenderer(action.presentation)
		prefix, err := renderer.withClauseSQL(ctx, view, definitions, parser.StatementWithRecursive(action.syntax), func(statement parser.Statement) (string, error) {
			if query, ok := statement.(*parser.Select); ok {
				project := renderer.queries[query]
				if project == nil {
					return "", fmt.Errorf("stuntgres: missing retained rule WITH body")
				}
				return renderer.selectQuerySQL(ctx, view, query, project)
			}
			for _, definition := range b.ctes {
				mutation, ok := mutationBinding(definition)
				if definition.Statement != statement || !ok {
					continue
				}
				presentation := action.modifierPresentations[statement]
				if presentation == nil {
					return "", fmt.Errorf("stuntgres: missing retained rule modifier presentation")
				}
				return ruleActionSQL(ctx, view, ruleAction{syntax: statement, binding: *mutation, presentation: presentation, modifierPresentations: action.modifierPresentations})
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
		if len(b.targets) != 0 {
			names := make([]string, len(b.targets))
			for i, slot := range b.targets {
				attribute := b.relation.Attributes[slot]
				names[i], err = deparseAttribute(d, attribute)
				if err != nil {
					return "", err
				}
				if len(b.arrayTargets) > i {
					names[i], err = ruleAssignmentSQL(ctx, view, names[i], b.arrayTargets[i], action)
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
		} else if action.presentation != nil && s.Source != nil && b.query != nil {
			text, err := action.presentation.selectSQL(ctx, view, viewPresentation{})
			if err != nil {
				return "", err
			}
			sql.WriteString(" " + text)
		} else {
			sql.WriteString(" VALUES ")
			for i, row := range b.expressions {
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
			if constraint := b.insert.conflict.constraint; constraint != (ids.ObjectAddress{}) {
				if constraint.ClassID != 2606 || constraint.SubID != 0 {
					return "", fmt.Errorf("stuntgres: invalid named rule constraint identity")
				}
				// Names belong to the current parent metadata, not the frozen
				// action's creation spelling or a newly reused name.
				canonicalName := constraintName(d, constraint)
				if canonicalName == "" {
					return "", fmt.Errorf("stuntgres: missing named rule constraint")
				}
				sql.WriteString(" ON CONSTRAINT " + catalog.QuoteIdentifier(canonicalName))
			} else if len(b.insert.conflict.keys) != 0 {
				parts := make([]string, len(b.insert.conflict.keys))
				for i, key := range b.insert.conflict.keys {
					if key.attribute != 0 {
						parts[i], err = deparseAttribute(d, key.attribute)
					} else {
						parts[i], err = ruleExpressionSQL(ctx, view, key.expression, action)
						parts[i] = "(" + parts[i] + ")"
					}
					if err != nil {
						return "", err
					}
					if key.collation != 0 {
						parts[i] += " COLLATE " + typedef.CollationSQLName(view, key.collation)
					}
					if key.class.Name != "" {
						parts[i] += " " + catalog.QuoteIdentifier("pg_catalog") + "." + catalog.QuoteIdentifier(key.class.Name)
					}
				}
				sql.WriteString(" (" + strings.Join(parts, ", ") + ")")
			}
			if b.insert.conflict.predicate != nil {
				text, err := ruleExpressionSQL(ctx, view, b.insert.conflict.predicate, action)
				if err != nil {
					return "", err
				}
				sql.WriteString(" WHERE " + text)
			}
			if b.insert.conflict.update == nil {
				sql.WriteString(" DO NOTHING")
			} else {
				sql.WriteString(" DO UPDATE SET ")
				for i, expr := range b.insert.conflict.update.assignments {
					if i != 0 {
						sql.WriteString(", ")
					}
					column, err := deparseAttribute(d, b.relation.Attributes[b.insert.conflict.updateTargets[i]])
					if err != nil {
						return "", err
					}
					if len(b.insert.conflict.update.arrayTargets) > i {
						column, err = ruleAssignmentSQL(ctx, view, column, b.insert.conflict.update.arrayTargets[i], action)
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
				if b.insert.conflict.update.predicate != nil {
					text, err := ruleExpressionSQL(ctx, view, b.insert.conflict.update.predicate, action)
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
		if b.update.from != nil {
			text, err := ruleFromSQL(ctx, view, b.update.from, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(" FROM " + text)
		}
		if b.update.predicate != nil {
			text, err := ruleExpressionSQL(ctx, view, b.update.predicate, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(" WHERE " + text)
		}
	case *parser.Delete:
		sql.WriteString("DELETE FROM " + name)
		if b.delete.using != nil {
			text, err := ruleFromSQL(ctx, view, b.delete.using, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(" USING " + text)
		}
		if b.predicate != nil {
			text, err := ruleExpressionSQL(ctx, view, b.predicate, action)
			if err != nil {
				return "", err
			}
			sql.WriteString(" WHERE " + text)
		}
	}
	if b.returning != nil {
		parts := make([]string, len(b.returning.expressions))
		for i, expr := range b.returning.expressions {
			parts[i], err = ruleExpressionSQL(ctx, view, expr, action)
			if err != nil {
				return "", err
			}
		}
		sql.WriteString(" RETURNING " + strings.Join(parts, ", "))
	}
	return sql.String(), nil
}

func ruleDumpGroups(ctx context.Context, view catalog.Read, roles authorization.Read) ([]dumpPolicyGroup, error) {
	var groups []dumpPolicyGroup
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(rulesOf(d)) == 0 {
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
		for _, r := range rulesOf(d) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, address := range r.references {
				address.SubID = 0
				var provider catalog.Descriptor
				if address.ClassID == 2606 {
					parent := constraintParent(view, address)
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
				renderer := newViewRenderer(r.conditionPresentation)
				renderer.frame = r.conditionPresentation.query.Root.(*projectNode).Frame
				text, err := renderer.expressionSQL(ctx, view, r.conditionPresentation.syntax.Targets[0], viewLiteralPath{clause: viewTargetLiteral})
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
					parts[i], err = ruleActionSQL(ctx, view, action)
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
		}
		groups = append(groups, dumpPolicyGroup{owner: owner.Name, table: table, sql: sql.String()})
	}
	slices.SortFunc(groups, func(a, b dumpPolicyGroup) int { return strings.Compare(a.table, b.table) })
	return groups, nil
}
