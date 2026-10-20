package engine

import (
	"context"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

func (r scalarRenderer) arraySQL(ctx context.Context, view catalog.Read, n plan.ArrayExpr) (string, error) {
	parts := make([]string, len(n.Elements))
	for i, child := range n.Elements {
		text, err := r.sql(ctx, view, child)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	text := "ARRAY[" + strings.Join(parts, ", ") + "]"
	if len(parts) == 0 {
		declaration, err := catalogTypeDeclarationAt(view, r.definition, n.Usage)
		if err != nil {
			return "", err
		}
		text += "::" + declaration
	}
	return text, nil
}

func (r scalarRenderer) subscriptSQL(ctx context.Context, view catalog.Read, n plan.SubscriptExpr) (string, error) {
	text, err := r.sql(ctx, view, n.Base)
	if err != nil {
		return "", err
	}
	text = "(" + text + ")"
	for _, s := range n.Subscripts {
		text += "["
		if s.Lower != nil {
			part, err := r.sql(ctx, view, s.Lower)
			if err != nil {
				return "", err
			}
			text += part
		}
		if s.Slice {
			text += ":"
		}
		if s.Upper != nil {
			part, err := r.sql(ctx, view, s.Upper)
			if err != nil {
				return "", err
			}
			text += part
		}
		text += "]"
	}
	return text, nil
}
