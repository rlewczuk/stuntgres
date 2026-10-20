package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type returningMode uint8

const (
	returningInsert returningMode = iota
	returningUpdate
	returningDelete
	returningMerge
)

// RETURNING adds projection namespaces, not additional physical sources.
type returningAliases struct {
	old, new string
	target   string
	mode     returningMode
}

type boundReturning struct {
	expressions        []expr.Expr
	schema             []catalog.Column
	regclassOutput     bool
	reads              []int
	readRequired       bool // Whole-row references retain SELECT demand with zero members.
	visibilitySnapshot bool
}

// Side-aware columns preserve dependencies even when their tuple is absent.
type returningColumnExpression struct {
	columnExpression
	old, absent bool
	merge       bool
	side        mergeReturningSide
	// A lowered logical view column can be an arbitrary physical expression.
	value expr.Expr
}

func returningRowContext(c *EvalContext, scope, frame uint32) (*EvalContext, error) {
	if err := evaluationContext(c); err != nil {
		return nil, err
	}
	for parent := c; parent != nil; parent = parent.Outer {
		if parent.Scope == scope && parent.Frame == frame {
			return parent, nil
		}
	}
	return nil, fmt.Errorf("stuntgres: RETURNING row frame is missing")
}

func (e returningColumnExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	parent, err := returningRowContext(c, e.scope, e.frame)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.merge && mergeReturningAbsent(parent, e.side) {
		return sqltype.Null(e.Usage.Type), nil
	}
	if e.absent || !e.merge && e.old && parent.OldRow == nil {
		return sqltype.Null(e.Usage.Type), nil
	}
	value := expr.Expr(e.columnExpression)
	if e.value != nil {
		value = e.value
	}
	if !e.old {
		return value.Eval(c)
	}
	old := *parent
	old.Context, old.Row = c.Context, parent.OldRow
	return value.Eval(&old)
}

func (b *expressionBinder) bindReturning(raw *parser.Returning, mode returningMode) (*boundReturning, error) {
	aliases := returningAliases{old: "old", new: "new", mode: mode}
	visible := b.target.name
	if b.target.alias != "" {
		visible = b.target.alias
	}
	aliases.target = visible
	var seenOld, seenNew bool
	for _, option := range raw.Aliases {
		seen, label := &seenOld, "OLD"
		if option.New {
			seen, label = &seenNew, "NEW"
		}
		if *seen {
			return nil, expr.ErrorAt("42601", label+" cannot be specified multiple times", option.Pos)
		}
		*seen = true
		if option.Alias.Text == visible {
			return nil, expr.ErrorAt("42712", fmt.Sprintf(`table name "%s" specified more than once`, option.Alias.Text), option.Pos)
		}
		if b.query.namespace != nil {
			for _, qualifier := range b.query.namespace.qualifiers {
				if qualifier.name == option.Alias.Text {
					return nil, expr.ErrorAt("42712", fmt.Sprintf(`table name "%s" specified more than once`, option.Alias.Text), option.Pos)
				}
			}
		}
		if option.New {
			aliases.new = option.Alias.Text
		} else {
			aliases.old = option.Alias.Text
		}
		if seenOld && seenNew && aliases.old == aliases.new {
			return nil, expr.ErrorAt("42712", fmt.Sprintf(`table name "%s" specified more than once`, option.Alias.Text), option.Pos)
		}
	}
	// Explicit aliases take precedence over the other implicit namespace.
	if aliases.old == aliases.new {
		if seenOld {
			aliases.new = ""
		} else {
			aliases.old = ""
		}
	}
	// Actual target/FROM relation qualifiers mask only implicit pseudo names.
	if b.query.namespace != nil {
		for _, qualifier := range b.query.namespace.qualifiers {
			if !seenOld && qualifier.name == aliases.old {
				aliases.old = ""
			}
			if !seenNew && qualifier.name == aliases.new {
				aliases.new = ""
			}
		}
	}
	previous, clause := b.target.returningAliases, b.clause.aggregateClause
	b.target.returningAliases, b.clause.aggregateClause = &aliases, "RETURNING"
	defer func() { b.target.returningAliases, b.clause.aggregateClause = previous, clause }()
	output := &boundReturning{}
	appendOutput := func(expression expr.Expr, name string) {
		descriptor := catalog.Column{Name: name, Usage: expression.ResultUsage()}
		switch column := expression.(type) {
		case columnExpression:
			if b.query.namespace != nil {
				descriptor = b.query.namespace.frame[column.slot]
			} else {
				descriptor = b.target.relation.Columns[column.slot]
			}
			if column.mergeSource {
				descriptor.Nullable, descriptor.NullabilityKnown = true, true
			}
		case returningColumnExpression:
			descriptor = b.target.relation.Columns[column.slot]
			if mode == returningMerge || column.absent || column.old && mode == returningInsert {
				descriptor.Nullable, descriptor.NullabilityKnown = true, true
			}
		case mergeActionExpression:
			descriptor.Nullable, descriptor.NullabilityKnown = false, true
		default:
			if metadata, ok := expression.(expr.ResultNullability); ok {
				if nullable, known := metadata.ResultNullability(expr.ResultReturning); known {
					descriptor.Nullable, descriptor.NullabilityKnown = nullable, true
				}
			}
		}
		descriptor.Name = name
		output.expressions = append(output.expressions, expression)
		output.schema = append(output.schema, descriptor)
	}
	for target, expression := range raw.Targets {
		if err := b.ctx.Err(); err != nil {
			return nil, err
		}
		if star, ok := expression.(parser.StarExpr); ok {
			if b.query.namespace != nil {
				if newValues, pseudo := b.returningPseudo(star.Qualifier); pseudo {
					for slot, column := range b.target.relation.Columns {
						appendOutput(b.returningColumnAt(slot, star.Pos, newValues), column.Name)
					}
				} else {
					columns := b.query.namespace.columns
					if len(star.Qualifier.Parts) != 0 {
						qualifier, err := b.queryQualifier(star.Qualifier, star.Pos)
						if err != nil {
							return nil, err
						}
						columns = qualifier.columns
					}
					if mode == returningMerge && len(star.Qualifier.Parts) == 0 {
						for _, column := range columns {
							if column.leaf != queryTargetLeaf {
								appendOutput(b.queryColumnAt(column, star.Pos), column.descriptor.Name)
							}
						}
						for _, column := range columns {
							if column.leaf == queryTargetLeaf {
								appendOutput(b.queryColumnAt(column, star.Pos), column.descriptor.Name)
							}
						}
					} else {
						for _, column := range columns {
							appendOutput(b.queryColumnAt(column, star.Pos), column.descriptor.Name)
						}
					}
				}
				continue
			}
			newValues, err := b.returningQualifier(star.Qualifier, star.Pos)
			if err != nil {
				return nil, err
			}
			for slot, column := range b.target.relation.Columns {
				appendOutput(b.returningColumnAt(slot, star.Pos, newValues), column.Name)
			}
		} else if field, ok := expression.(parser.FieldExpr); ok && field.Expansion {
			expansion, err := b.expandComposite(field)
			if err != nil {
				return nil, err
			}
			for i, expr := range expansion.fields {
				appendOutput(expr, expansion.names[i])
			}
		} else {
			expr, err := b.bindOutput(expression, false)
			if err != nil {
				return nil, err
			}
			name, _ := expressionName(expression, expr.ResultUsage(), expr)
			if target < len(raw.Labels) && raw.Labels[target].Text != "" {
				name = raw.Labels[target].Text
			}
			appendOutput(expr, name)
		}
	}
	if len(output.schema) == 0 {
		pos := raw.Targets[0].SourcePosition()
		if field, ok := raw.Targets[0].(parser.FieldExpr); ok && field.OpenPos.Character != 0 {
			pos = field.OpenPos
		}
		return nil, expr.ErrorAt("42601", "RETURNING must have at least one column", pos)
	}
	// Resolve output occurrences only after every target has constrained the
	// shared parameter slots, matching ordinary scalar projection ordering.
	for i, expr := range output.expressions {
		expr, err := b.finalizeProjection(expr)
		if err != nil {
			return nil, err
		}
		output.expressions[i] = expr
		output.schema[i].Usage = expr.ResultUsage()
		output.regclassOutput = output.regclassOutput || expr.ResultUsage().Type.ContainsCatalogIdentifier()
	}
	output.visibilitySnapshot = b.consumer.visibilitySnapshot
	return output, nil
}

func (b *expressionBinder) returningQualifier(name parser.ObjectName, pos parser.Position) (bool, error) {
	if len(name.Parts) == 0 {
		return b.target.returningAliases.mode != returningDelete, nil
	}
	visible := b.target.name
	if b.target.alias != "" {
		visible = b.target.alias
	}
	label := name.Parts[len(name.Parts)-1].Text
	if len(name.Parts) == 1 {
		if label == visible {
			return b.target.returningAliases.mode != returningDelete, nil
		}
		if label == b.target.returningAliases.old {
			return false, nil
		}
		if label == b.target.returningAliases.new {
			return true, nil
		}
	} else if len(name.Parts) == 2 && b.target.alias == "" && label == b.target.name {
		namespace := b.engine.catalog.state.Get(b.target.relation.Namespace())
		if namespace != nil && name.Parts[0].Text == namespace.Name() {
			return b.target.returningAliases.mode != returningDelete, nil
		}
	}
	if label == b.target.name && b.target.alias != "" {
		err := expr.ErrorAt("42P01", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, label), pos).(*sqlstate.Error)
		err.Hint = fmt.Sprintf(`Perhaps you meant to reference the table alias "%s".`, b.target.alias)
		return false, err
	}
	return false, expr.ErrorAt("42P01", fmt.Sprintf(`missing FROM-clause entry for table "%s"`, label), pos)
}

func (b *expressionBinder) returningColumnAt(slot int, pos parser.Position, newValues bool) expr.Expr {
	column := b.columnAt(slot, pos).(columnExpression)
	if b.target.returningAliases.mode == returningMerge {
		side := mergeOldSide
		if newValues {
			side = mergeNewSide
		}
		return returningColumnExpression{columnExpression: column, old: !newValues, merge: true, side: side}
	}
	if !newValues && b.target.returningAliases.mode != returningDelete {
		return returningColumnExpression{columnExpression: column, old: true}
	}
	if newValues && b.target.returningAliases.mode == returningDelete {
		return returningColumnExpression{columnExpression: column, absent: true}
	}
	return column
}

func (b *expressionBinder) returningPseudo(name parser.ObjectName) (newValues, pseudo bool) {
	if len(name.Parts) != 1 {
		return false, false
	}
	label := name.Parts[0].Text
	if b.target.returningAliases.old != "" && label == b.target.returningAliases.old {
		return false, true
	}
	if b.target.returningAliases.new != "" && label == b.target.returningAliases.new {
		return true, true
	}
	return false, false
}

func (b *expressionBinder) returningColumn(raw parser.ColumnExpr) (expr.Expr, error) {
	parts := raw.Name.Parts
	if len(parts) == 0 || len(parts) > 3 {
		return nil, fmt.Errorf("stuntgres: column outside expression scope")
	}
	if len(parts) == 1 {
		_, column := b.target.relation.Index[parts[0].Text]
		if b.query.namespace != nil {
			for _, candidate := range b.query.namespace.columns {
				column = column || candidate.descriptor.Name == parts[0].Text
			}
		}
		if !column {
			if expr, found, err := b.returningWholeRow(parts[0].Text, b.target.relation, b.frameID(), b.target.returningAliases, raw.Pos); found {
				return expr, err
			}
		}
		_, pseudo := b.returningPseudo(raw.Name)
		visible := b.target.name
		if b.target.alias != "" {
			visible = b.target.alias
		}
		if !column && (pseudo || parts[0].Text == visible) {
			return b.wholeRow(raw.Name, raw.Pos, true)
		}
	}
	if b.query.namespace != nil {
		if _, pseudo := b.returningPseudo(parser.ObjectName{Parts: parts[:len(parts)-1]}); !pseudo {
			return b.queryColumn(raw)
		}
	}
	newValues, err := b.returningQualifier(parser.ObjectName{Parts: parts[:len(parts)-1]}, raw.Pos)
	if err != nil {
		return nil, err
	}
	name := parts[len(parts)-1].Text
	slot, ok := b.target.relation.Index[name]
	if !ok {
		if len(parts) > 1 {
			return nil, expr.ErrorAt("42703", fmt.Sprintf("column %s.%s does not exist", parts[len(parts)-2].Text, name), raw.Pos)
		}
		return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	return b.returningColumnAt(slot, raw.Pos, newValues), nil
}

func preflightReturning(c *EvalContext, output *boundReturning) (*boundReturning, error) {
	var expressions []expr.Expr
	for i, bound := range output.expressions {
		// Plan genuine SQL constants before importing frame coercions and before
		// tuple/index demand. Row-dependent output still executes after insertion
		// preparation; an imported parameter must not become a SQL constant.
		planned, changed, err := simplifyConditional(c, bound, false)
		if err != nil {
			return nil, err
		}
		folded, converted, err := preflightCoercions(c, planned)
		changed = changed || converted
		if err != nil {
			return nil, err
		}
		if changed {
			if expressions == nil {
				expressions = append([]expr.Expr(nil), output.expressions...)
			}
			expressions[i] = folded
		}
	}
	if expressions == nil {
		return output, nil
	}
	private := *output
	private.expressions = expressions
	return &private, nil
}

// Pseudo namespaces participate at their actual lexical RETURNING scope.
// Ordinary nearer FROM aliases shadow them; a child query never changes the
// statement's target relation or OLD/NEW row ownership.
func (b *expressionBinder) outerReturningScope(name parser.ObjectName) (*queryBindingScope, bool) {
	if len(name.Parts) != 1 {
		return nil, false
	}
	label := name.Parts[0].Text
	if b.query.namespace != nil {
		for _, q := range b.query.namespace.qualifiers {
			if q.name == label {
				return nil, false
			}
		}
	}
	for i := len(b.query.scopes) - 1; i >= 0; i-- {
		scope := &b.query.scopes[i]
		if !scope.allowed {
			continue
		}
		if scope.namespace != nil {
			for _, q := range scope.namespace.qualifiers {
				if q.name == label {
					return nil, false
				}
			}
		}
		if scope.returning == nil {
			continue
		}
		if scope.returning.old != "" && label == scope.returning.old {
			return scope, false
		}
		if scope.returning.new != "" && label == scope.returning.new {
			return scope, true
		}
	}
	return nil, false
}

func (b *expressionBinder) outerReturningColumnAt(scope *queryBindingScope, slot int, pos parser.Position, newValues bool) expr.Expr {
	column := b.columnAtOf(scope.relation, slot, pos).(columnExpression)
	column.frame = scope.frame
	if scope.returning.mode == returningMerge {
		side := mergeOldSide
		if newValues {
			side = mergeNewSide
		}
		return returningColumnExpression{columnExpression: column, old: !newValues, merge: true, side: side}
	}
	if !newValues && scope.returning.mode != returningDelete {
		return returningColumnExpression{columnExpression: column, old: true}
	}
	if newValues && scope.returning.mode == returningDelete {
		return returningColumnExpression{columnExpression: column, absent: true}
	}
	return column
}

// Scalar transition values and their default-row member projections share the
// same saved column wrappers and owning frame as correlated qualified columns.
func (b *expressionBinder) outerReturningWholeRow(scope *queryBindingScope, pos parser.Position, newValues bool) (expr.Expr, error) {
	if scope.returning.mode == returningInsert || scope.returning.mode == returningMerge {
		name := scope.returning.old
		if newValues {
			name = scope.returning.new
		}
		expr, _, err := b.returningWholeRow(name, scope.relation, scope.frame, scope.returning, pos)
		return expr, err
	}
	fields := make([]expr.Expr, len(scope.relation.Columns))
	props := expr.Properties{}
	for i := range fields {
		fields[i] = b.outerReturningColumnAt(scope, i, pos, newValues)
		props = expr.MergeProperties(props, fields[i].Properties())
	}
	typ, _ := b.bindingCatalog().LookupType(scope.relation.RowType)
	return rowExpression{Info: b.info(typ.Usage(), pos, props), fields: fields, returningWhole: true, returningNull: scope.returning.mode == returningDelete && newValues}, nil
}
