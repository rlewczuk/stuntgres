package proc

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
)

// declarationCallOrder links the existing parsed declaration's expression
// preorder to its typed source parts once, when the immutable recipe is built.
// It never resolves a SQL name or constructs an executable expression.
func declarationCallOrder(statements []parser.Statement, recipe DeclarationRecipe) ([]int, error) {
	parts := recipe.Parts
	used := make([]bool, len(parts))
	positions := make([]int, len(parts))
	offset := 0
	if recipe.Return {
		offset = len("SELECT ")
	}
	for i, part := range parts {
		positions[i] = offset
		offset += len(part.source())
	}
	var order []int
	for _, statement := range statements {
		err := parser.VisitQueryExpressions(statement, func(raw parser.Expr) error {
			var name string
			count := 0
			operator := false
			switch call := raw.(type) {
			case parser.FunctionExpr:
				if len(call.Name.Parts) == 0 {
					return nil
				}
				name = call.Name.Parts[len(call.Name.Parts)-1].Text
				count = len(call.Args)
			case parser.CallExpr:
				if len(call.Name.Parts) == 0 {
					return nil
				}
				name = call.Name.Parts[len(call.Name.Parts)-1].Text
				count = len(call.Args)
			case parser.OperatorExpr:
				if len(call.Name.Parts) == 0 {
					return nil
				}
				name = call.Name.Parts[len(call.Name.Parts)-1].Text
				count = len(call.Args)
				operator = true
			default:
				return nil
			}
			for i, part := range parts {
				if recipe.Source == "" && positions[i] != raw.SourcePosition().Offset {
					continue
				}
				if used[i] {
					continue
				}
				matches := false
				if operator {
					matches = part.Kind == DeclarationOperator && part.Name == name && part.ArgumentCount == count
				} else {
					switch part.Kind {
					case DeclarationRoutine:
						arguments := 0
						if part.Signature != "" {
							arguments = strings.Count(part.Signature, ",") + 1
						}
						matches = part.Name == name && arguments == count
					case DeclarationExtensionMember:
						spelling := part.Text
						if at := strings.LastIndexByte(spelling, '.'); at >= 0 {
							spelling = spelling[at+1:]
						}
						matches = part.Class == 1255 && spelling == name && part.ArgumentCount == count
					}
				}
				if matches {
					used[i] = true
					order = append(order, i)
					return nil
				}
			}
			// SQL special syntax can contain parser calls without a typed callable slot
			// (for example the declaration-only SQL forms). Such syntax confers nothing.
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for i, part := range parts {
		callable := part.Kind == DeclarationRoutine || part.Kind == DeclarationOperator || part.Kind == DeclarationExtensionMember && part.Class == 1255
		if callable && !used[i] {
			return nil, fmt.Errorf("stuntgres: unmatched typed declaration call %d", i)
		}
	}
	return order, nil
}
