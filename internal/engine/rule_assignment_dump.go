package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
)

func ruleAssignmentSQL(ctx context.Context, view catalog.Read, column string, target *arrayAssignmentTarget, action ruleAction) (string, error) {
	var sql strings.Builder
	sql.WriteString(column)
	for ; target != nil; target = target.Next {
		if target.Attribute != 0 {
			index, ok := target.Container.Type.BaseType().CompositeAttributeIndex(target.Attribute)
			if !ok {
				return "", fmt.Errorf("stuntgres: retired rule assignment attribute")
			}
			sql.WriteString("." + catalog.QuoteIdentifier(target.Container.Type.BaseType().CompositeMember(index).Name))
			continue
		}
		for _, sub := range target.Node.Subscripts {
			sql.WriteByte('[')
			if target.Node.Slice {
				if sub.Lower != nil {
					text, err := ruleExpressionSQL(ctx, view, sub.Lower, action)
					if err != nil {
						return "", err
					}
					sql.WriteString(text)
				}
				sql.WriteByte(':')
			}
			if sub.Upper != nil {
				text, err := ruleExpressionSQL(ctx, view, sub.Upper, action)
				if err != nil {
					return "", err
				}
				sql.WriteString(text)
			}
			sql.WriteByte(']')
		}
	}
	return sql.String(), nil
}
