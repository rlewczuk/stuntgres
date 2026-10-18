package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func ruleRelationSQL(view CatalogRead, ref ids.ObjectRef) (string, error) {
	d, ok := view.Relation(ref.Address)
	if !ok {
		return "", ErrCatalogReplaced
	}
	ns, ok := view.Namespace(d.namespace)
	if !ok {
		return "", ErrCatalogReplaced
	}
	return quoteIdentifier(ns.name) + "." + quoteIdentifier(d.name), nil
}

func ruleExpressionSQL(ctx context.Context, view CatalogRead, bound expr.Expr, action ruleAction, sourceRoots ...LogicalNode) (string, error) {
	r := pgRenderer{view: view, showImplicit: true}
	r.columnSQL = func(column columnExpression) (string, error) {
		if column.scope == 1 && column.frame == 1 && len(sourceRoots) != 0 {
			return ruleSourceColumnSQL(view, sourceRoots[0], column.slot, action, func(root LogicalNode, expr expr.Expr) (string, error) {
				return ruleExpressionSQL(ctx, view, expr, action, root)
			})
		}
		if column.scope != rulePseudoScope && column.scope == 1 && column.frame == 1 && action.binding.relation != nil {
			offset := len(action.binding.relation.columns)
			var from LogicalNode
			if action.binding.update != nil {
				from = action.binding.update.from
			} else {
				from = action.binding.deleteUsing
			}
			if from != nil && column.slot >= offset {
				return ruleSourceColumnSQL(view, from, column.slot-offset, action, func(root LogicalNode, expr expr.Expr) (string, error) {
					return ruleExpressionSQL(ctx, view, expr, action, root)
				})
			}
		}
		a := column.origin.Address
		a.SubID = 0
		d, ok := view.Relation(a)
		if !ok {
			return "", ErrCatalogReplaced
		}
		name, err := deparseAttribute(d, column.origin.Address.SubID)
		if err != nil {
			return "", err
		}
		if conflict := action.binding.conflict; conflict != nil && conflict.update != nil &&
			column.scope == 1 && column.frame == 1 && column.slot >= conflict.width && column.slot < 2*conflict.width &&
			a == action.binding.ref.Address {
			return "EXCLUDED." + name, nil
		}
		if column.scope == rulePseudoScope {
			side := "OLD"
			if column.frame == 2 {
				side = "NEW"
			}
			return side + "." + name, nil
		}
		if column.queryOccurrence > 0 {
			return quoteIdentifier("_rule_source_"+strconv.Itoa(column.queryOccurrence-1)) + "." + name, nil
		}
		return quoteIdentifier(d.name) + "." + name, nil
	}
	r.nodeSQL = func(expr expr.Expr) (string, bool, error) {
		subquery, ok := expr.(subqueryExpression)
		if !ok {
			return "", false, nil
		}
		if action.presentation == nil {
			return "", true, fmt.Errorf("stuntgres: missing rule subquery presentation")
		}
		project, ok := subquery.queryPlan.Root.(*projectNode)
		if !ok || project.syntax == nil {
			return "", true, fmt.Errorf("stuntgres: missing rule subquery syntax")
		}
		renderer := newViewRenderer(action.presentation)
		text, err := renderer.selectQuerySQL(ctx, view, project.syntax, project)
		if err != nil {
			return "", true, err
		}
		if subquery.kind == parser.SubqueryExists {
			return "EXISTS (" + text + ")", true, nil
		}
		if subquery.kind == parser.SubqueryScalar || subquery.kind == parser.SubqueryRow {
			return "(" + text + ")", true, nil
		}
		parts := make([]string, len(subquery.comparisons))
		for i, comparison := range subquery.comparisons {
			parts[i], err = r.sql(ctx, comparison.left, pgRenderParent{})
			if err != nil {
				return "", true, err
			}
		}
		left := strings.Join(parts, ", ")
		if len(parts) > 1 {
			left = "ROW(" + left + ")"
		}
		quantifier := "ANY"
		if subquery.kind == parser.SubqueryAll {
			quantifier = "ALL"
		}
		text = "(" + left + " " + subquery.operator + " " + quantifier + " (" + text + "))"
		if subquery.negate {
			text = "NOT " + text
		}
		return text, true, nil
	}
	node, _, err := rewriteViewExpression(view, bound, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
		if c, ok := expr.(returningColumnExpression); ok {
			return c.columnExpression, true, nil
		}
		return expr, false, nil
	})
	if err != nil {
		return "", err
	}
	return r.sql(ctx, node, pgRenderParent{})
}

func ruleFromSQL(ctx context.Context, view CatalogRead, root LogicalNode, action ruleAction) (string, error) {
	if root == nil {
		return "", nil
	}
	switch n := root.(type) {
	case *sourceNode:
		name, err := ruleRelationSQL(view, n.ref)
		if err != nil {
			return "", err
		}
		return name + " AS " + quoteIdentifier("_rule_source_"+strconv.Itoa(n.occurrence)), nil
	case *joinNode:
		left, err := ruleFromSQL(ctx, view, n.left, action)
		if err != nil {
			return "", err
		}
		right, err := ruleFromSQL(ctx, view, n.right, action)
		if err != nil {
			return "", err
		}
		if n.predicate == nil {
			return "(" + left + " CROSS JOIN " + right + ")", nil
		}
		predicate, err := ruleExpressionSQL(ctx, view, n.predicate, action, n)
		if err != nil {
			return "", err
		}
		kind := "INNER"
		switch n.kind {
		case parser.JoinLeft:
			kind = "LEFT"
		case parser.JoinRight:
			kind = "RIGHT"
		case parser.JoinFull:
			kind = "FULL"
		}
		return "(" + left + " " + kind + " JOIN " + right + " ON " + predicate + ")", nil
	case *derivedNode:
		if n.cte != nil {
			return quoteIdentifier(n.cte.name) + " AS " + quoteIdentifier("_rule_source_"+strconv.Itoa(n.ordinal)), nil
		}
		project, ok := n.query.Root.(*projectNode)
		if !ok || project.syntax == nil || action.presentation == nil {
			return "", fmt.Errorf("stuntgres: missing rule derived-query presentation")
		}
		renderer := newViewRenderer(action.presentation)
		text, err := renderer.selectQuerySQL(ctx, view, project.syntax, project)
		if err != nil {
			return "", err
		}
		return "(" + text + ") AS " + quoteIdentifier("_rule_source_"+strconv.Itoa(n.ordinal)), nil
	case *functionFromNode:
		parts := make([]string, len(n.calls))
		for i, call := range n.calls {
			var err error
			parts[i], err = ruleExpressionSQL(ctx, view, call, action)
			if err != nil {
				return "", err
			}
		}
		text := "ROWS FROM (" + strings.Join(parts, ", ") + ")"
		if n.ordinality {
			text += " WITH ORDINALITY"
		}
		if n.correlated {
			text = "LATERAL " + text
		}
		names := make([]string, len(n.Schema()))
		for i, column := range n.Schema() {
			names[i] = quoteIdentifier(column.Name)
		}
		return text + " AS " + quoteIdentifier("_rule_function_"+strconv.Itoa(n.viewOrdinal)) + "(" + strings.Join(names, ", ") + ")", nil
	case *valuesNode:
		var rows []string
		for _, row := range n.rows {
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
			names[i] = quoteIdentifier(column.Name)
		}
		return "(VALUES " + strings.Join(rows, ", ") + ") AS " + quoteIdentifier("_rule_values") + "(" + strings.Join(names, ", ") + ")", nil
	case *singletonNode:
		return "", nil
	}
	return "", fmt.Errorf("stuntgres: missing rule FROM presentation for %T", root)
}

func ruleActionSQL(ctx context.Context, view CatalogRead, action ruleAction) (string, error) {
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
				if definition.statement != statement || definition.mutation == nil {
					continue
				}
				presentation := action.modifierPresentations[statement]
				if presentation == nil {
					return "", fmt.Errorf("stuntgres: missing retained rule modifier presentation")
				}
				return ruleActionSQL(ctx, view, ruleAction{syntax: statement, binding: *definition.mutation, presentation: presentation, modifierPresentations: action.modifierPresentations})
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
				attribute := b.relation.attributes[slot]
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
			if constraint := b.conflict.constraint; constraint != (ids.ObjectAddress{}) {
				if constraint.ClassID != 2606 || constraint.SubID != 0 {
					return "", fmt.Errorf("stuntgres: invalid named rule constraint identity")
				}
				// Names belong to the current parent metadata, not the frozen
				// action's creation spelling or a newly reused name.
				canonicalName := constraintName(d, constraint)
				if canonicalName == "" {
					return "", fmt.Errorf("stuntgres: missing named rule constraint")
				}
				sql.WriteString(" ON CONSTRAINT " + quoteIdentifier(canonicalName))
			} else if len(b.conflict.keys) != 0 {
				parts := make([]string, len(b.conflict.keys))
				for i, key := range b.conflict.keys {
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
						parts[i] += " COLLATE " + collationSQLName(view, key.collation)
					}
					if key.class.Name != "" {
						parts[i] += " " + quoteIdentifier("pg_catalog") + "." + quoteIdentifier(key.class.Name)
					}
				}
				sql.WriteString(" (" + strings.Join(parts, ", ") + ")")
			}
			if b.conflict.predicate != nil {
				text, err := ruleExpressionSQL(ctx, view, b.conflict.predicate, action)
				if err != nil {
					return "", err
				}
				sql.WriteString(" WHERE " + text)
			}
			if b.conflict.update == nil {
				sql.WriteString(" DO NOTHING")
			} else {
				sql.WriteString(" DO UPDATE SET ")
				for i, expr := range b.conflict.update.assignments {
					if i != 0 {
						sql.WriteString(", ")
					}
					column, err := deparseAttribute(d, b.relation.attributes[b.conflict.updateTargets[i]])
					if err != nil {
						return "", err
					}
					if len(b.conflict.update.arrayTargets) > i {
						column, err = ruleAssignmentSQL(ctx, view, column, b.conflict.update.arrayTargets[i], action)
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
				if b.conflict.update.predicate != nil {
					text, err := ruleExpressionSQL(ctx, view, b.conflict.update.predicate, action)
					if err != nil {
						return "", err
					}
					sql.WriteString(" WHERE " + text)
				}
			}
		}
	case *parser.Update:
		sql.WriteString("UPDATE " + name + " SET ")
		for i, expr := range b.update.assignments {
			if i != 0 {
				sql.WriteString(", ")
			}
			column, err := deparseAttribute(d, b.relation.attributes[b.targets[i]])
			if err != nil {
				return "", err
			}
			if len(b.update.arrayTargets) > i {
				column, err = ruleAssignmentSQL(ctx, view, column, b.update.arrayTargets[i], action)
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
		if b.deleteUsing != nil {
			text, err := ruleFromSQL(ctx, view, b.deleteUsing, action)
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

func ruleDumpGroups(ctx context.Context, view CatalogRead, roles authorization.Read) ([]dumpPolicyGroup, error) {
	var groups []dumpPolicyGroup
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*relationDescriptor)
		if !ok || len(d.rules) == 0 {
			continue
		}
		ns := view.MustNamespace(d.namespace)
		if ns.temporary != (ids.SessionToken{}) {
			continue
		}
		owner, err := roles.Descriptor(d.owner)
		if err != nil {
			return nil, err
		}
		table, err := ruleRelationSQL(view, ids.ObjectRef{Address: d.address})
		if err != nil {
			return nil, err
		}
		var sql strings.Builder
		for _, r := range d.rules {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, address := range r.references {
				address.SubID = 0
				var provider Descriptor
				if address.ClassID == 2606 {
					parent := constraintParent(view, address)
					if parent == nil {
						return nil, ErrCatalogReplaced
					}
					provider = parent
				} else {
					provider = view.Get(address)
				}
				if provider == nil {
					return nil, ErrCatalogReplaced
				}
				if namespace, ok := view.Namespace(provider.Namespace()); ok && namespace.temporary != (ids.SessionToken{}) {
					return nil, fmt.Errorf("stuntgres: dump cannot serialize a rule referencing a temporary relation")
				}
			}
			sql.WriteString("CREATE RULE " + quoteIdentifier(r.Name) + " AS ON " + r.event + " TO " + table)
			if r.condition != nil {
				renderer := newViewRenderer(r.conditionPresentation)
				renderer.frame = r.conditionPresentation.query.Root.(*projectNode).frame
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
