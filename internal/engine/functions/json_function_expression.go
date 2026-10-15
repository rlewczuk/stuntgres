package functions

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

type JSONKeys[E Expression[C], C Context, P any] struct {
	Info[P]
	Plan sqltype.JSONKeysPlan
	Args []E
}

func (e JSONKeys[E, C, P]) Eval(c C) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSON keys set call")
}
func (e JSONKeys[E, C, P]) Reconstruct(info Info[P], args []E) (JSONKeys[E, C, P], error) {
	plan, err := sqltype.BindJSONKeys(e.Plan.Name(), args[0].ResultUsage(), info.Usage)
	return JSONKeys[E, C, P]{info, plan, args}, err
}

type JSONFunctionDefinition struct {
	Input, Result  sqltype.ID
	Keys           bool
	Each           bool
	Elements       bool
	ArgumentIntent CoercionIntent
}

func JSONFunctionCall(name string, visible bool, count, names int) (JSONFunctionDefinition, Resolution) {
	if !visible || count != 1 || names != 0 {
		return JSONFunctionDefinition{}, Missing
	}
	definition := JSONFunctionDefinition{Input: sqltype.JSON, Result: sqltype.Text, ArgumentIntent: LikeArgument}
	switch name {
	case "json_typeof":
	case "jsonb_typeof", "jsonb_pretty":
		definition.Input = sqltype.JSONB
	case "json_array_length":
		definition.Result = sqltype.Int4
	case "jsonb_array_length":
		definition.Input, definition.Result = sqltype.JSONB, sqltype.Int4
	case "json_object_keys":
		definition.Keys = true
	case "jsonb_object_keys":
		definition.Input, definition.Keys = sqltype.JSONB, true
	case "json_each", "json_each_text":
		definition.Each = true
	case "json_array_elements", "json_array_elements_text":
		definition.Elements = true
		if name == "json_array_elements" {
			definition.Result = sqltype.JSON
		}
	case "jsonb_array_elements", "jsonb_array_elements_text":
		definition.Input, definition.Result, definition.Elements = sqltype.JSONB, sqltype.JSONB, true
		if name == "jsonb_array_elements_text" {
			definition.Result = sqltype.Text
		}
	default:
		return JSONFunctionDefinition{}, Missing
	}
	return definition, Resolved
}
func ResolveJSONArgument(argument, input sqltype.Usage) Resolution {
	if argument.Type != nil {
		if _, err := sqltype.PlanCoercion(argument, input, sqltype.Implicit); err != nil {
			return Missing
		}
	}
	return Resolved
}

func NewJSONKeys[E Expression[C], C Context, P any](info Info[P], name string, arg E) (JSONKeys[E, C, P], error) {
	plan, err := sqltype.BindJSONKeys(name, arg.ResultUsage(), info.Usage)
	if err != nil {
		return JSONKeys[E, C, P]{}, err
	}
	return JSONKeys[E, C, P]{info, plan, []E{arg}}, nil
}

type JSONEach[E Expression[C], C Context, P any] struct {
	Info[P]
	Plan sqltype.JSONEachPlan
	Args []E
}

func (e JSONEach[E, C, P]) Eval(c C) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSON each set call")
}
func (e JSONEach[E, C, P]) Reconstruct(info Info[P], args []E) (JSONEach[E, C, P], error) {
	if len(args) != 1 {
		return JSONEach[E, C, P]{}, fmt.Errorf("invalid JSON each arguments")
	}
	plan, err := sqltype.BindJSONEach(e.Plan.Name(), args[0].ResultUsage(), info.Usage)
	return JSONEach[E, C, P]{info, plan, args}, err
}

func NewJSONEach[E Expression[C], C Context, P any](info Info[P], name string, arg E) (JSONEach[E, C, P], error) {
	plan, err := sqltype.BindJSONEach(name, arg.ResultUsage(), info.Usage)
	if err != nil {
		return JSONEach[E, C, P]{}, err
	}
	return JSONEach[E, C, P]{info, plan, []E{arg}}, nil
}

type JSONElements[E Expression[C], C Context, P any] struct {
	Info[P]
	Plan sqltype.JSONElementsPlan
	Args []E
}

func (e JSONElements[E, C, P]) Eval(c C) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSON elements set call")
}

func (e JSONElements[E, C, P]) Reconstruct(info Info[P], args []E) (JSONElements[E, C, P], error) {
	if len(args) != 1 {
		return JSONElements[E, C, P]{}, fmt.Errorf("invalid JSON elements arguments")
	}
	plan, err := sqltype.BindJSONElements(e.Plan.Name(), args[0].ResultUsage(), info.Usage)
	return JSONElements[E, C, P]{info, plan, args}, err
}

func NewJSONElements[E Expression[C], C Context, P any](info Info[P], name string, arg E) (JSONElements[E, C, P], error) {
	plan, err := sqltype.BindJSONElements(name, arg.ResultUsage(), info.Usage)
	if err != nil {
		return JSONElements[E, C, P]{}, err
	}
	return JSONElements[E, C, P]{info, plan, []E{arg}}, nil
}

type JSONBElements[E Expression[C], C Context, P any] struct {
	Info[P]
	Plan sqltype.JSONBElementsPlan
	Args []E
}

func (e JSONBElements[E, C, P]) Eval(c C) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSONB elements set call")
}
func (e JSONBElements[E, C, P]) Reconstruct(info Info[P], args []E) (JSONBElements[E, C, P], error) {
	if len(args) != 1 {
		return JSONBElements[E, C, P]{}, fmt.Errorf("invalid JSONB elements arguments")
	}
	plan, err := sqltype.BindJSONBElements(e.Plan.Name(), args[0].ResultUsage(), info.Usage)
	return JSONBElements[E, C, P]{info, plan, args}, err
}
func NewJSONBElements[E Expression[C], C Context, P any](info Info[P], name string, arg E) (JSONBElements[E, C, P], error) {
	plan, err := sqltype.BindJSONBElements(name, arg.ResultUsage(), info.Usage)
	if err != nil {
		return JSONBElements[E, C, P]{}, err
	}
	return JSONBElements[E, C, P]{info, plan, []E{arg}}, nil
}
