package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
)

// Definition/presentation paths retain written labels/order and omitted defaults;
// execution and dependency walkers use declared slots, never this display order.
func datetimeMakeSQL(e functions.DatetimeMake, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	if e.Count < 1 || e.Count > 7 || e.WrittenCount < 0 || e.WrittenCount > e.Count {
		return "", fmt.Errorf("invalid stored datetime constructor")
	}
	parts := make([]string, e.WrittenCount)
	for i := range e.WrittenCount {
		slot := int(e.WrittenSlots[i])
		if slot >= e.Count || e.Args[slot] == nil {
			return "", fmt.Errorf("invalid stored constructor argument")
		}
		text, err := render(e.Args[slot])
		if err != nil {
			return "", err
		}
		if e.Labels[i] != "" {
			text = identifier(e.Labels[i]) + " => " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}
