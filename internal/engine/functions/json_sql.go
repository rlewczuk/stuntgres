package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
)

// The stored-SQL renderers serve the engine's stored scalar SQL switch and
// MapJSONExtractChildren its preflight switch. They stay exported until those
// switches adopt the node protocol (deparse uses expr.Renderable).

// JSONStripNullsSQL renders a stored json_strip_nulls call in written order.
func JSONStripNullsSQL(e JSONStripNulls, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	if e.WrittenCount < 1 || e.WrittenCount > 2 {
		return "", fmt.Errorf("invalid stored JSON stripping")
	}
	var local [2]string
	parts := local[:e.WrittenCount]
	var supplied [2]bool
	for i := range e.WrittenCount {
		slot := int(e.WrittenSlots[i])
		if slot >= 2 || supplied[slot] || e.Args[slot] == nil {
			return "", fmt.Errorf("invalid stored JSON stripping argument")
		}
		supplied[slot] = true
		text, err := render(e.Args[slot])
		if err != nil {
			return "", err
		}
		if e.Labels[i] != "" {
			text = identifier(e.Labels[i]) + " => " + text
		}
		if e.Variadic && i == e.WrittenCount-1 {
			text = "VARIADIC " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}

func JSONExtractSQL(e JSONExtract, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	parts := make([]string, len(e.Args))
	for i, arg := range e.Args {
		text, err := render(arg)
		if err != nil {
			return "", err
		}
		if len(e.Labels) > 0 && e.Labels[i] != "" {
			text = identifier(e.Labels[i]) + " => " + text
		}
		if e.Variadic && i == len(e.Args)-1 {
			text = "VARIADIC " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}

// MapJSONExtractChildren leaves the original immutable child slice untouched and
// only allocates when a child actually changes.
func MapJSONExtractChildren(e JSONExtract, mapChild func(expr.Expr) (expr.Expr, bool, error)) (JSONExtract, bool, error) {
	var args []expr.Expr
	for i, old := range e.Args {
		next, changed, err := mapChild(old)
		if err != nil {
			return e, false, err
		}
		if changed && args == nil {
			args = append([]expr.Expr(nil), e.Args...)
		}
		if args != nil {
			args[i] = next
		}
	}
	if args != nil {
		e.Args = args
	}
	return e, args != nil, nil
}

func JSONBPathQueryArraySQL(e JSONBPathQueryArray, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	if e.WrittenCount < 2 || e.WrittenCount > 4 {
		return "", fmt.Errorf("invalid stored JSONPath array query")
	}
	parts := make([]string, e.WrittenCount)
	var supplied [4]bool
	for i := range e.WrittenCount {
		slot := int(e.WrittenSlots[i])
		if slot >= 4 || supplied[slot] || e.Args[slot] == nil {
			return "", fmt.Errorf("invalid stored JSONPath array query argument")
		}
		supplied[slot] = true
		text, err := render(e.Args[slot])
		if err != nil {
			return "", err
		}
		if e.Labels[i] != "" {
			text = identifier(e.Labels[i]) + " => " + text
		}
		if e.Variadic && i == e.WrittenCount-1 {
			text = "VARIADIC " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}

func JSONBPathExistsSQL(e JSONBPathExists, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	return jsonbPathQueryCallSQL(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, name, render, identifier)
}

func JSONBPathMatchSQL(e JSONBPathMatch, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	return jsonbPathQueryCallSQL(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, name, render, identifier)
}

func JSONBPathQueryFirstSQL(e JSONBPathQueryFirst, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	return jsonbPathQueryCallSQL(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, name, render, identifier)
}

func JSONBPathQuerySQL(e JSONBPathQuery, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	return jsonbPathQueryCallSQL(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, name, render, identifier)
}

func jsonbPathQueryCallSQL(args [4]expr.Expr, count int, slots [4]uint8, labels [4]string, variadic bool, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	if count < 2 || count > 4 {
		return "", fmt.Errorf("invalid stored JSONPath query")
	}
	parts := make([]string, count)
	var supplied [4]bool
	for i := range count {
		slot := int(slots[i])
		if slot >= 4 || supplied[slot] || args[slot] == nil {
			return "", fmt.Errorf("invalid stored JSONPath query argument")
		}
		supplied[slot] = true
		text, err := render(args[slot])
		if err != nil {
			return "", err
		}
		if labels[i] != "" {
			text = identifier(labels[i]) + " => " + text
		}
		if variadic && i == count-1 {
			text = "VARIADIC " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}

func JSONBSetSQL(e JSONBSet, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	if e.Count < 4 || e.Count > 5 || e.WrittenCount < 3 || e.WrittenCount > e.Count {
		return "", fmt.Errorf("invalid stored JSONB replacement")
	}
	parts := make([]string, e.WrittenCount)
	var supplied [5]bool
	for i := range e.WrittenCount {
		slot := int(e.WrittenSlots[i])
		if slot >= e.Count || supplied[slot] || e.Args[slot] == nil {
			return "", fmt.Errorf("invalid stored JSONB replacement argument")
		}
		supplied[slot] = true
		text, err := render(e.Args[slot])
		if err != nil {
			return "", err
		}
		if e.Labels[i] != "" {
			text = identifier(e.Labels[i]) + " => " + text
		}
		if e.Variadic && i == e.WrittenCount-1 {
			text = "VARIADIC " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}
