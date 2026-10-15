package engine

import (
	"context"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// sqlJSONResult retains enclosing casts at the constructor's representation boundary.
// RETURNING ownership comes from the conversion plan, never its assignment mode.
func sqlJSONResult(node BoundExpr) bool {
	if conversion, ok := node.(coercionExpression); ok && conversion.plan.IsJSONReturning() {
		node = conversion.child
	}
	_, ok := node.(sqlJSONExpression)
	return ok
}

func sqlJSONSource(arg BoundExpr) BoundExpr {
	if conversion, ok := arg.(coercionExpression); ok && conversion.plan.IsJSONFormattedInput() {
		return conversion.child
	}
	return arg
}

func sqlJSONRecipe(raw parser.SQLJSONExpr, parts []string, query, returning string) string {
	text := query
	if raw.Query == nil {
		if raw.Kind == "json_object" {
			members := make([]string, 0, len(parts)/2)
			for i := 0; i < len(parts); i += 2 {
				members = append(members, "("+parts[i]+") VALUE "+parts[i+1])
			}
			text = strings.Join(members, ", ")
		} else {
			text = strings.Join(parts, ", ")
		}
	} else if len(raw.Formats) > 0 {
		text += jsonAggregateFormatSQL(raw.Formats[0])
	}
	objectMembers := raw.Kind == "json_object" && len(parts) != 0
	if raw.Query == nil && (raw.Kind == "json_array" || objectMembers) {
		if raw.Absent {
			text += " ABSENT ON NULL"
		} else {
			text += " NULL ON NULL"
		}
	}
	if raw.Unique && (raw.Kind != "json_object" || objectMembers) {
		text += " WITH UNIQUE KEYS"
	}
	if returning != "" {
		if text != "" {
			text += " "
		}
		text += "RETURNING " + returning + jsonAggregateFormatSQL(raw.Output)
	}
	return strings.ToUpper(raw.Kind) + "(" + text + ")"
}
func (r pgRenderer) sqlJSON(ctx context.Context, n sqlJSONExpression, returning sqltype.Usage) (string, error) {
	parts := make([]string, len(n.args))
	for i, arg := range n.args {
		var err error
		parts[i], err = r.sql(ctx, sqlJSONSource(arg), pgRenderParent{functionArgument: true})
		if err != nil {
			return "", err
		}
		if i < len(n.raw.Formats) {
			parts[i] += jsonAggregateFormatSQL(n.raw.Formats[i])
		}
	}
	declaration := ""
	if returning.Type != nil {
		var err error
		declaration, err = r.typeSQL(returning)
		if err != nil {
			return "", err
		}
	}
	return sqlJSONRecipe(n.raw, parts, "", declaration), nil
}
func jsonPredicateRecipe(child, kind string, unique, not bool) string {
	text := child + " IS "
	if not {
		text += "NOT "
	}
	text += "JSON"
	if kind != "" {
		text += " " + strings.ToUpper(kind)
	}
	if unique {
		text += " WITH UNIQUE KEYS"
	}
	return "(" + text + ")"
}

func (r scalarRenderer) sqlJSON(ctx context.Context, view CatalogRead, n sqlJSONExpression, returning sqltype.Usage) (string, error) {
	parts := make([]string, len(n.args))
	for i, arg := range n.args {
		text, err := r.sql(ctx, view, sqlJSONSource(arg))
		if err != nil {
			return "", err
		}
		parts[i] = text
		if i < len(n.raw.Formats) {
			parts[i] += jsonAggregateFormatSQL(n.raw.Formats[i])
		}
	}
	declaration := ""
	if returning.Type != nil {
		var err error
		declaration, err = catalogTypeDeclarationAt(view, r.definition, returning)
		if err != nil {
			return "", err
		}
	}
	return sqlJSONRecipe(n.raw, parts, "", declaration), nil
}
