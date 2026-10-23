package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type returningMode uint8

const (
	returningInsert returningMode = iota
	returningUpdate
	ReturningDelete
	returningMerge
)

// RETURNING adds projection namespaces, not additional physical sources.
type returningAliases struct {
	old, new string
	target   string
	mode     returningMode
}

func (b *Binder) BindReturning(raw *parser.Returning, mode returningMode) (*Returning, error) {
	aliases := returningAliases{old: "old", new: "new", mode: mode}
	visible := b.Target.Name
	if b.Target.alias != "" {
		visible = b.Target.alias
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
		if b.Query.Namespace != nil {
			for _, qualifier := range b.Query.Namespace.Qualifiers {
				if qualifier.Name == option.Alias.Text {
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
	if b.Query.Namespace != nil {
		for _, qualifier := range b.Query.Namespace.Qualifiers {
			if !seenOld && qualifier.Name == aliases.old {
				aliases.old = ""
			}
			if !seenNew && qualifier.Name == aliases.new {
				aliases.new = ""
			}
		}
	}
	previous, clause := b.Target.returningAliases, b.Clause.AggregateClause
	b.Target.returningAliases, b.Clause.AggregateClause = &aliases, "RETURNING"
	defer func() { b.Target.returningAliases, b.Clause.AggregateClause = previous, clause }()
	output := &Returning{}
	appendOutput := func(expression expr.Expr, name string) {
		descriptor := catalog.Column{Name: name, Usage: expression.ResultUsage()}
		switch column := expression.(type) {
		case plan.ColumnExpr:
			if b.Query.Namespace != nil {
				descriptor = b.Query.Namespace.Frame[column.Slot]
			} else {
				descriptor = b.Target.Relation.Columns[column.Slot]
			}
			if column.MergeSource {
				descriptor.Nullable, descriptor.NullabilityKnown = true, true
			}
		case plan.ReturningColumnExpr:
			descriptor = b.Target.Relation.Columns[column.Slot]
			if mode == returningMerge || column.Absent || column.Old && mode == returningInsert {
				descriptor.Nullable, descriptor.NullabilityKnown = true, true
			}
		case plan.MergeActionExpr:
			descriptor.Nullable, descriptor.NullabilityKnown = false, true
		default:
			if metadata, ok := expression.(expr.ResultNullability); ok {
				if nullable, known := metadata.ResultNullability(expr.ResultReturning); known {
					descriptor.Nullable, descriptor.NullabilityKnown = nullable, true
				}
			}
		}
		descriptor.Name = name
		output.Expressions = append(output.Expressions, expression)
		output.Schema = append(output.Schema, descriptor)
	}
	for target, expression := range raw.Targets {
		if err := b.Ctx.Err(); err != nil {
			return nil, err
		}
		if star, ok := expression.(parser.StarExpr); ok {
			if b.Query.Namespace != nil {
				if newValues, pseudo := b.Target.returningPseudo(star.Qualifier); pseudo {
					for slot, column := range b.Target.Relation.Columns {
						appendOutput(b.returningColumnAt(slot, star.Pos, newValues), column.Name)
					}
				} else {
					columns := b.Query.Namespace.Columns
					if len(star.Qualifier.Parts) != 0 {
						qualifier, err := b.queryQualifier(star.Qualifier, star.Pos)
						if err != nil {
							return nil, err
						}
						columns = qualifier.Columns
					}
					if mode == returningMerge && len(star.Qualifier.Parts) == 0 {
						for _, column := range columns {
							if column.Leaf != QueryTargetLeaf {
								appendOutput(b.QueryColumnAt(column, star.Pos), column.Descriptor.Name)
							}
						}
						for _, column := range columns {
							if column.Leaf == QueryTargetLeaf {
								appendOutput(b.QueryColumnAt(column, star.Pos), column.Descriptor.Name)
							}
						}
					} else {
						for _, column := range columns {
							appendOutput(b.QueryColumnAt(column, star.Pos), column.Descriptor.Name)
						}
					}
				}
				continue
			}
			newValues, err := b.returningQualifier(star.Qualifier, star.Pos)
			if err != nil {
				return nil, err
			}
			for slot, column := range b.Target.Relation.Columns {
				appendOutput(b.returningColumnAt(slot, star.Pos, newValues), column.Name)
			}
		} else if field, ok := expression.(parser.FieldExpr); ok && field.Expansion {
			expansion, err := b.ExpandComposite(field)
			if err != nil {
				return nil, err
			}
			for i, expr := range expansion.Fields {
				appendOutput(expr, expansion.Names[i])
			}
		} else {
			expr, err := b.bindOutput(expression, false)
			if err != nil {
				return nil, err
			}
			name, _ := ExpressionName(expression, expr.ResultUsage(), expr)
			if target < len(raw.Labels) && raw.Labels[target].Text != "" {
				name = raw.Labels[target].Text
			}
			appendOutput(expr, name)
		}
	}
	if len(output.Schema) == 0 {
		pos := raw.Targets[0].SourcePosition()
		if field, ok := raw.Targets[0].(parser.FieldExpr); ok && field.OpenPos.Character != 0 {
			pos = field.OpenPos
		}
		return nil, expr.ErrorAt("42601", "RETURNING must have at least one column", pos)
	}
	// Resolve output occurrences only after every target has constrained the
	// shared parameter slots, matching ordinary scalar projection ordering.
	for i, expr := range output.Expressions {
		expr, err := b.FinalizeProjection(expr)
		if err != nil {
			return nil, err
		}
		output.Expressions[i] = expr
		output.Schema[i].Usage = expr.ResultUsage()
		output.RegclassOutput = output.RegclassOutput || expr.ResultUsage().Type.ContainsCatalogIdentifier()
	}
	output.VisibilitySnapshot = b.Consumer.VisibilitySnapshot
	return output, nil
}

func (b *Binder) returningQualifier(name parser.ObjectName, pos parser.Position) (bool, error) {
	if len(name.Parts) == 0 {
		return b.Target.returningAliases.mode != ReturningDelete, nil
	}
	visible := b.Target.Name
	if b.Target.alias != "" {
		visible = b.Target.alias
	}
	label := name.Parts[len(name.Parts)-1].Text
	if len(name.Parts) == 1 {
		if label == visible {
			return b.Target.returningAliases.mode != ReturningDelete, nil
		}
		if label == b.Target.returningAliases.old {
			return false, nil
		}
		if label == b.Target.returningAliases.new {
			return true, nil
		}
	} else if len(name.Parts) == 2 && b.Target.alias == "" && label == b.Target.Name {
		namespace, _ := b.Host.CurrentCatalog().Descriptor(b.Target.Relation.Namespace())
		if namespace != nil && name.Parts[0].Text == namespace.Name() {
			return b.Target.returningAliases.mode != ReturningDelete, nil
		}
	}
	if label == b.Target.Name && b.Target.alias != "" {
		err := expr.ErrorAt("42P01", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, label), pos).(*sqlstate.Error)
		err.Hint = fmt.Sprintf(`Perhaps you meant to reference the table alias "%s".`, b.Target.alias)
		return false, err
	}
	return false, expr.ErrorAt("42P01", fmt.Sprintf(`missing FROM-clause entry for table "%s"`, label), pos)
}

func (b *Binder) returningColumnAt(slot int, pos parser.Position, newValues bool) expr.Expr {
	column := b.columnAt(slot, pos).(plan.ColumnExpr)
	if b.Target.returningAliases.mode == returningMerge {
		side := exec.MergeOldSide
		if newValues {
			side = exec.MergeNewSide
		}
		return plan.ReturningColumnExpr{ColumnExpr: column, Old: !newValues, Merge: true, Side: side}
	}
	if !newValues && b.Target.returningAliases.mode != ReturningDelete {
		return plan.ReturningColumnExpr{ColumnExpr: column, Old: true}
	}
	if newValues && b.Target.returningAliases.mode == ReturningDelete {
		return plan.ReturningColumnExpr{ColumnExpr: column, Absent: true}
	}
	return column
}

func (t *targetContext) returningPseudo(name parser.ObjectName) (newValues, pseudo bool) {
	if len(name.Parts) != 1 {
		return false, false
	}
	label := name.Parts[0].Text
	if t.returningAliases.old != "" && label == t.returningAliases.old {
		return false, true
	}
	if t.returningAliases.new != "" && label == t.returningAliases.new {
		return true, true
	}
	return false, false
}

func (b *Binder) returningColumn(raw parser.ColumnExpr) (expr.Expr, error) {
	parts := raw.Name.Parts
	if len(parts) == 0 || len(parts) > 3 {
		return nil, fmt.Errorf("stuntgres: column outside expression scope")
	}
	if len(parts) == 1 {
		_, column := b.Target.Relation.Index[parts[0].Text]
		if b.Query.Namespace != nil {
			for _, candidate := range b.Query.Namespace.Columns {
				column = column || candidate.Descriptor.Name == parts[0].Text
			}
		}
		if !column {
			if expr, found, err := b.returningWholeRow(parts[0].Text, b.Target.Relation, b.FrameID(), b.Target.returningAliases, raw.Pos); found {
				return expr, err
			}
		}
		_, pseudo := b.Target.returningPseudo(raw.Name)
		visible := b.Target.Name
		if b.Target.alias != "" {
			visible = b.Target.alias
		}
		if !column && (pseudo || parts[0].Text == visible) {
			return b.wholeRow(raw.Name, raw.Pos, true)
		}
	}
	if b.Query.Namespace != nil {
		if _, pseudo := b.Target.returningPseudo(parser.ObjectName{Parts: parts[:len(parts)-1]}); !pseudo {
			return b.queryColumn(raw)
		}
	}
	newValues, err := b.returningQualifier(parser.ObjectName{Parts: parts[:len(parts)-1]}, raw.Pos)
	if err != nil {
		return nil, err
	}
	name := parts[len(parts)-1].Text
	slot, ok := b.Target.Relation.Index[name]
	if !ok {
		if len(parts) > 1 {
			return nil, expr.ErrorAt("42703", fmt.Sprintf("column %s.%s does not exist", parts[len(parts)-2].Text, name), raw.Pos)
		}
		return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	return b.returningColumnAt(slot, raw.Pos, newValues), nil
}

// Pseudo namespaces participate at their actual lexical RETURNING scope.
// Ordinary nearer FROM aliases shadow them; a child query never changes the
// statement's target relation or OLD/NEW row ownership.
func (b *Binder) outerReturningScope(name parser.ObjectName) (*queryBindingScope, bool) {
	if len(name.Parts) != 1 {
		return nil, false
	}
	label := name.Parts[0].Text
	if b.Query.Namespace != nil {
		for _, q := range b.Query.Namespace.Qualifiers {
			if q.Name == label {
				return nil, false
			}
		}
	}
	for i := len(b.Query.Scopes) - 1; i >= 0; i-- {
		scope := &b.Query.Scopes[i]
		if !scope.allowed {
			continue
		}
		if scope.namespace != nil {
			for _, q := range scope.namespace.Qualifiers {
				if q.Name == label {
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

func (b *Binder) outerReturningColumnAt(scope *queryBindingScope, slot int, pos parser.Position, newValues bool) expr.Expr {
	column := b.columnAtOf(scope.relation, slot, pos).(plan.ColumnExpr)
	column.Frame = scope.frame
	if scope.returning.mode == returningMerge {
		side := exec.MergeOldSide
		if newValues {
			side = exec.MergeNewSide
		}
		return plan.ReturningColumnExpr{ColumnExpr: column, Old: !newValues, Merge: true, Side: side}
	}
	if !newValues && scope.returning.mode != ReturningDelete {
		return plan.ReturningColumnExpr{ColumnExpr: column, Old: true}
	}
	if newValues && scope.returning.mode == ReturningDelete {
		return plan.ReturningColumnExpr{ColumnExpr: column, Absent: true}
	}
	return column
}

// Scalar transition values and their default-row member projections share the
// same saved column wrappers and owning frame as correlated qualified columns.
func (b *Binder) outerReturningWholeRow(scope *queryBindingScope, pos parser.Position, newValues bool) (expr.Expr, error) {
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
	typ, _ := b.BindingCatalog().LookupType(scope.relation.RowType)
	return plan.RowExpr{Info: b.Info(typ.Usage(), pos, props), Fields: fields, ReturningWhole: true, ReturningNull: scope.returning.mode == ReturningDelete && newValues}, nil
}
