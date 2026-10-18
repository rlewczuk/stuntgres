package functions

import "stuntgres/internal/engine/expr"
import "stuntgres/internal/sqltype"

type TextObjectDefinition struct {
	Result, Argument sqltype.ID
	Intent           CoercionIntent
}

func ResolveTextObject(name string, visible bool, count int) (TextObjectDefinition, Resolution) {
	if !visible || count < 1 || count > 2 {
		return TextObjectDefinition{}, Missing
	}
	definition := TextObjectDefinition{Result: sqltype.JSON, Argument: 1009, Intent: LikeArgument}
	if name == "jsonb_object" {
		definition.Result = sqltype.JSONB
	}
	return definition, Resolved
}
func TextObjectArgumentsViable(arguments []Argument, target sqltype.Usage) bool {
	for _, argument := range arguments {
		if argument.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(argument.Usage, target, sqltype.Implicit); err != nil {
				return false
			}
		}
	}
	return true
}
func CoerceTextObjectArguments(b ArgumentCoercer, arguments []Argument, target sqltype.Usage, intent CoercionIntent) ([]expr.Expr, error) {
	args := make([]expr.Expr, len(arguments))
	for i, argument := range arguments {
		node, err := b.CoerceArgument(argument, CoercionRequest{Intent: intent, Target: target})
		if err != nil {
			return nil, err
		}
		args[i] = node
	}
	return args, nil
}
