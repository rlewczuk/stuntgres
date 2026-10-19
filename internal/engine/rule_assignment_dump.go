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
	for ; target != nil; target = target.next {
		if target.attribute != 0 {
			index, ok := target.container.Type.BaseType().CompositeAttributeIndex(target.attribute)
			if !ok {
				return "", fmt.Errorf("stuntgres: retired rule assignment attribute")
			}
			sql.WriteString("." + catalog.QuoteIdentifier(target.container.Type.BaseType().CompositeMember(index).Name))
			continue
		}
		for _, sub := range target.node.subscripts {
			sql.WriteByte('[')
			if target.node.slice {
				if sub.lower != nil {
					text, err := ruleExpressionSQL(ctx, view, sub.lower, action)
					if err != nil {
						return "", err
					}
					sql.WriteString(text)
				}
				sql.WriteByte(':')
			}
			if sub.upper != nil {
				text, err := ruleExpressionSQL(ctx, view, sub.upper, action)
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
