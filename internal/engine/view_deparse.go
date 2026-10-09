package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (v *viewDefinition) selectSQL(ctx context.Context, catalog CatalogRead) (string, error) {
	s := v.syntax
	renderer := newViewRenderer(v)
	var from string
	if s.From != nil {
		source, err := renderer.fromSQL(ctx, catalog, s.From)
		if err != nil {
			return "", err
		}
		from = source.text
	}
	var targets []string
	for i, expr := range s.Targets {
		if _, star := expr.(parser.StarExpr); star {
			for _, slot := range v.stars[i] {
				text, err := renderer.slotSQL(ctx, catalog, viewFromRoot(v.query.Root), slot)
				if err != nil {
					return "", err
				}
				targets = append(targets, text)
			}
			continue
		}
		text, err := renderer.expressionSQL(ctx, catalog, expr, viewLiteralPath{clause: viewTargetLiteral, target: i})
		if err != nil {
			return "", err
		}
		targets = append(targets, text)
	}
	for i := range targets {
		targets[i] += " AS " + quoteIdentifier(v.query.Root.Schema()[i].Name)
	}
	text := "SELECT " + strings.Join(targets, ", ")
	if s.From != nil {
		text += " FROM " + from
	}
	if s.Where != nil {
		part, err := renderer.expressionSQL(ctx, catalog, s.Where, viewLiteralPath{clause: viewWhereLiteral})
		if err != nil {
			return "", err
		}
		text += " WHERE " + part
	}
	if len(s.GroupBy) > 0 {
		var keys []string
		for i, expr := range s.GroupBy {
			part, err := renderer.expressionSQL(ctx, catalog, expr, viewLiteralPath{clause: viewGroupLiteral, target: i})
			if err != nil {
				return "", err
			}
			keys = append(keys, part)
		}
		text += " GROUP BY " + strings.Join(keys, ", ")
	}
	if s.Having != nil {
		part, err := renderer.expressionSQL(ctx, catalog, s.Having, viewLiteralPath{clause: viewHavingLiteral})
		if err != nil {
			return "", err
		}
		text += " HAVING " + part
	}
	if s.Order != nil {
		keys := make([]string, len(s.Order.Keys))
		for i, key := range s.Order.Keys {
			path := viewLiteralPath{clause: viewOrderLiteral, target: i}
			name, err := renderer.expressionSQL(ctx, catalog, key.Expr, path)
			if err != nil {
				return "", err
			}
			if key.Desc {
				name += " DESC"
			}
			switch key.Nulls {
			case parser.NullsFirst:
				name += " NULLS FIRST"
			case parser.NullsLast:
				name += " NULLS LAST"
			}
			keys[i] = name
		}
		text += " ORDER BY " + strings.Join(keys, ", ")
	}
	if s.Bounds != nil {
		for _, bound := range []struct {
			expr   parser.Expr
			clause viewLiteralClause
			name   string
		}{
			{s.Bounds.Offset, viewOffsetLiteral, "OFFSET"},
			{s.Bounds.Count, viewLimitLiteral, "LIMIT"},
		} {
			if bound.expr == nil {
				continue
			}
			part, err := renderer.expressionSQL(ctx, catalog, bound.expr, viewLiteralPath{clause: bound.clause})
			if err != nil {
				return "", err
			}
			text += " " + bound.name + " " + part
		}
	}
	if s.Locking != nil {
		text += " " + s.Locking.Mode
	}
	return text, nil
}

func (v *viewRenderer) expressionSQL(ctx context.Context, catalog CatalogRead, raw parser.Expr, path viewLiteralPath) (string, error) {
	ordinal := 0
	return v.expressionSQLAt(ctx, catalog, raw, path, &ordinal)
}

func (v *viewRenderer) expressionSQLAt(ctx context.Context, catalog CatalogRead, raw parser.Expr, path viewLiteralPath, ordinal *int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path.node = *ordinal
	(*ordinal)++
	if literal, ok := v.literals[path]; ok {
		_, identity := v.sequenceRefs[raw.SourcePosition().Offset]
		if literal.ResultUsage().Type.ID() != sqltype.Regclass || !identity {
			return literalSQL(ctx, literal)
		}
	}
	child := func(expr parser.Expr) (string, error) { return v.expressionSQLAt(ctx, catalog, expr, path, ordinal) }
	args := func(expressions []parser.Expr) (string, error) {
		parts := make([]string, len(expressions))
		for i, expr := range expressions {
			text, err := child(expr)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, ", "), nil
	}
	switch e := raw.(type) {
	case parser.IdentityExpr:
		return e.Name, nil
	case parser.LiteralExpr:
		if ref, ok := v.sequenceRefs[e.Value.Pos.Offset]; ok {
			d, exists := catalog.state.descriptors[ref.Address].(*relationDescriptor)
			if !exists {
				return "'" + strconv.FormatUint(uint64(ref.Address.ObjectID), 10) + "'", nil
			}
			name := quoteIdentifier(catalog.state.descriptors[d.namespace].Name()) + "." + quoteIdentifier(d.name)
			return "'" + strings.ReplaceAll(name, "'", "''") + "'", nil
		}
		if (path.clause == viewGroupLiteral || path.clause == viewOrderLiteral) && path.node == 0 && e.Value.Kind == parser.IntegerValue {
			return strconv.FormatInt(e.Value.Integer, 10), nil
		}
		return "", fmt.Errorf("stuntgres: missing analyzed view literal")
	case parser.ColumnExpr:
		slot, ok := v.columns[path]
		if !ok {
			return "", fmt.Errorf("stuntgres: missing view column occurrence")
		}
		root := viewFromRoot(v.query.Root)
		if path.clause == viewJoinLiteral {
			root = viewJoinAt(root, path.target)
		}
		return v.slotSQL(ctx, catalog, root, slot)
	case parser.TypedLiteralExpr:
		return "", fmt.Errorf("stuntgres: missing analyzed typed view literal")
	case parser.TimeZoneExpr:
		value, err := child(e.Value)
		if err != nil {
			return "", err
		}
		if e.Local {
			return "((" + value + ") AT LOCAL)", nil
		}
		zone, err := child(e.Zone)
		if err != nil {
			return "", err
		}
		return "((" + value + ") AT TIME ZONE (" + zone + "))", nil
	case parser.CollateExpr:
		text, err := child(e.Child)
		if err != nil {
			return "", err
		}
		return "(" + text + " COLLATE pg_catalog." + quoteIdentifier(e.Name.Parts[len(e.Name.Parts)-1].Text) + ")", nil
	case parser.CastExpr:
		literalPath := path
		literalPath.node = *ordinal
		text, err := child(e.Child)
		if err != nil {
			return "", err
		}
		if rawLiteral, ok := e.Child.(parser.LiteralExpr); ok && (rawLiteral.Value.Kind == parser.NullValue || rawLiteral.Value.Kind == parser.StringValue) {
			if literal, ok := v.literals[literalPath]; ok && literal.ResultUsage().Type.ID() == sqltype.Oid {
				// The binder types these literal children directly as the cast
				// target. OID literalSQL already supplies the canonical cast;
				// wrapping it again would grow another cast on every replay.
				return text, nil
			}
		}
		return "CAST(" + text + " AS " + syntaxTypeSQL(e.TypeName) + ")", nil
	case parser.OperatorExpr:
		left, err := child(e.Args[0])
		if err != nil {
			return "", err
		}
		op := e.Name.Parts[len(e.Name.Parts)-1].Text
		if len(e.Name.Parts) == 2 {
			op = "OPERATOR(" + quoteIdentifier(e.Name.Parts[0].Text) + "." + op + ")"
		}
		if len(e.Args) == 1 {
			return "(" + op + " " + left + ")", nil
		}
		right, err := child(e.Args[1])
		if err != nil {
			return "", err
		}
		return "(" + left + " " + op + " " + right + ")", nil
	case parser.LogicalExpr:
		left, err := child(e.Left)
		if err != nil {
			return "", err
		}
		if e.Op == parser.LogicalNot {
			return "(NOT " + left + ")", nil
		}
		right, err := child(e.Right)
		if err != nil {
			return "", err
		}
		op := "AND"
		if e.Op == parser.LogicalOr {
			op = "OR"
		}
		return "(" + left + " " + op + " " + right + ")", nil
	case parser.PredicateExpr:
		left, err := child(e.Left)
		if err != nil {
			return "", err
		}
		operators := []string{"IS NULL", "IS NOT NULL", "IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN", "IS DISTINCT FROM", "IS NOT DISTINCT FROM"}
		text := "(" + left + " " + operators[e.Kind]
		if e.Right != nil {
			right, err := child(e.Right)
			if err != nil {
				return "", err
			}
			text += " " + right
		}
		return text + ")", nil
	case parser.AggregateExpr:
		name := e.Name
		if e.Schema != nil {
			name = quoteIdentifier(e.Schema.Text) + "." + quoteIdentifier(e.Name)
		}
		if e.Star {
			return name + "(*)", nil
		}
		text, err := args(e.Args)
		return name + "(" + text + ")", err
	case parser.ConditionalExpr:
		text, err := args(e.Args)
		return e.Kind.String() + "(" + text + ")", err
	case parser.CaseExpr:
		text := "CASE"
		if e.Test != nil {
			part, err := child(e.Test)
			if err != nil {
				return "", err
			}
			text += " " + part
		}
		for _, arm := range e.Arms {
			condition, err := child(arm.Condition)
			if err != nil {
				return "", err
			}
			result, err := child(arm.Result)
			if err != nil {
				return "", err
			}
			text += " WHEN " + condition + " THEN " + result
		}
		if e.Else != nil {
			part, err := child(e.Else)
			if err != nil {
				return "", err
			}
			text += " ELSE " + part
		}
		return text + " END", nil
	case parser.FunctionExpr:
		name := e.Name.Parts[len(e.Name.Parts)-1].Text
		if parser.IdentityFunctionName(name) && len(e.Args) == 0 {
			// A bound identity call is a pg_catalog builtin. In particular,
			// decoded quoted keyword names cannot be replayed unqualified.
			return "pg_catalog." + name + "()", nil
		}
		if e.StringSyntax == parser.TrimStringSyntax {
			if len(e.Args) < 1 || len(e.Args) > 2 {
				return "", fmt.Errorf("stuntgres: invalid stored TRIM arity")
			}
			source, err := child(e.Args[0])
			if err != nil {
				return "", err
			}
			set := ""
			if len(e.Args) == 2 {
				set, err = child(e.Args[1])
				if err != nil {
					return "", err
				}
			}
			return trimSyntaxSQL(name, source, set, len(e.Args))
		}
		if e.Syntax == parser.SQLFunctionSyntax {
			return renderBinaryCall(name, e.Syntax, e.Args, "pg_catalog."+name, child)
		}
		if name == "position" {
			text, err := args(e.Args)
			return "pg_catalog.position(" + text + ")", err
		}
		text, err := args(e.Args)
		switch name {
		case "btrim", "ltrim", "rtrim", "normalize", "is_normalized", "lower", "upper", "casefold", "initcap":
			return "pg_catalog." + name + "(" + text + ")", err
		}
		return objectNameText(e.Name) + "(" + text + ")", err
	case parser.CallExpr:
		text, err := args(e.Args)
		return objectNameText(e.Name) + "(" + text + ")", err
	case parser.RegclassExpr:
		text, err := child(e.Child)
		return "CAST(" + text + " AS regclass)", err
	}
	return "", fmt.Errorf("stuntgres: unsupported view deparser expression %T", raw)
}

func syntaxTypeSQL(t parser.TypeName) string {
	name := t.Name
	if t.Quoted {
		name = quoteIdentifier(name)
	}
	if t.Schema != nil {
		name = quoteIdentifier(t.Schema.Text) + "." + quoteIdentifier(t.Name)
	}
	if len(t.Modifiers) > 0 {
		parts := make([]string, len(t.Modifiers))
		for i, m := range t.Modifiers {
			if m.Integer != nil {
				parts[i] = strconv.FormatInt(int64(*m.Integer), 10)
			} else {
				parts[i] = m.Raw
			}
		}
		name += "(" + strings.Join(parts, ",") + ")"
	}
	return name
}
