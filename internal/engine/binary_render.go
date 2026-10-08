package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
)

// renderBinaryCall shares special-form spelling between catalog and replay consumers.
func renderBinaryCall[T any](name string, syntax parser.FunctionSyntax, args []T, ordinary string, child func(T) (string, error)) (string, error) {
	parts := make([]string, len(args))
	for i, arg := range args {
		text, err := child(arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	if syntax == parser.OrdinaryFunctionSyntax {
		return ordinary + "(" + strings.Join(parts, ", ") + ")", nil
	}
	switch name {
	case "overlay":
		if len(parts) != 3 && len(parts) != 4 {
			break
		}
		text := "OVERLAY(" + parts[0] + " PLACING " + parts[1] + " FROM " + parts[2]
		if len(parts) == 4 {
			text += " FOR " + parts[3]
		}
		return text + ")", nil
	case "substring":
		if len(parts) != 2 && len(parts) != 3 {
			break
		}
		text := "SUBSTRING(" + parts[0] + " FROM " + parts[1]
		if len(parts) == 3 {
			text += " FOR " + parts[2]
		}
		return text + ")", nil
	case "position":
		if len(parts) != 2 {
			break
		}
		return "POSITION((" + parts[1] + ") IN (" + parts[0] + "))", nil
	case "btrim", "ltrim", "rtrim":
		if len(parts) != 2 {
			break
		}
		direction := "BOTH"
		if name == "ltrim" {
			direction = "LEADING"
		}
		if name == "rtrim" {
			direction = "TRAILING"
		}
		return "TRIM(" + direction + " " + parts[1] + " FROM " + parts[0] + ")", nil
	}
	return "", fmt.Errorf("stuntgres: invalid SQL binary syntax")
}
