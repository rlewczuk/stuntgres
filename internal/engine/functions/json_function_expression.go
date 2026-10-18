package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type JSONKeys struct {
	expr.Info
	Plan sqltype.JSONKeysPlan
	Args []expr.Expr
}

func (e JSONKeys) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSON keys set call")
}
func (e JSONKeys) Reconstruct(info expr.Info, args []expr.Expr) (JSONKeys, error) {
	plan, err := sqltype.BindJSONKeys(e.Plan.Name(), args[0].ResultUsage(), info.Usage)
	return JSONKeys{info, plan, args}, err
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
	case "jsonb_each", "jsonb_each_text":
		definition.Input, definition.Each = sqltype.JSONB, true
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

func NewJSONKeys(info expr.Info, name string, arg expr.Expr) (JSONKeys, error) {
	plan, err := sqltype.BindJSONKeys(name, arg.ResultUsage(), info.Usage)
	if err != nil {
		return JSONKeys{}, err
	}
	return JSONKeys{info, plan, []expr.Expr{arg}}, nil
}

type JSONEach struct {
	expr.Info
	Plan sqltype.JSONEachPlan
	Args []expr.Expr
}

func (e JSONEach) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSON each set call")
}
func (e JSONEach) Reconstruct(info expr.Info, args []expr.Expr) (JSONEach, error) {
	if len(args) != 1 {
		return JSONEach{}, fmt.Errorf("invalid JSON each arguments")
	}
	plan, err := sqltype.BindJSONEach(e.Plan.Name(), args[0].ResultUsage(), info.Usage)
	return JSONEach{info, plan, args}, err
}

func NewJSONEach(info expr.Info, name string, arg expr.Expr) (JSONEach, error) {
	plan, err := sqltype.BindJSONEach(name, arg.ResultUsage(), info.Usage)
	if err != nil {
		return JSONEach{}, err
	}
	return JSONEach{info, plan, []expr.Expr{arg}}, nil
}

type JSONElements struct {
	expr.Info
	Plan sqltype.JSONElementsPlan
	Args []expr.Expr
}

func (e JSONElements) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSON elements set call")
}

func (e JSONElements) Reconstruct(info expr.Info, args []expr.Expr) (JSONElements, error) {
	if len(args) != 1 {
		return JSONElements{}, fmt.Errorf("invalid JSON elements arguments")
	}
	plan, err := sqltype.BindJSONElements(e.Plan.Name(), args[0].ResultUsage(), info.Usage)
	return JSONElements{info, plan, args}, err
}

func NewJSONElements(info expr.Info, name string, arg expr.Expr) (JSONElements, error) {
	plan, err := sqltype.BindJSONElements(name, arg.ResultUsage(), info.Usage)
	if err != nil {
		return JSONElements{}, err
	}
	return JSONElements{info, plan, []expr.Expr{arg}}, nil
}

type JSONBElements struct {
	expr.Info
	Plan sqltype.JSONBElementsPlan
	Args []expr.Expr
}

func (e JSONBElements) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSONB elements set call")
}
func (e JSONBElements) Reconstruct(info expr.Info, args []expr.Expr) (JSONBElements, error) {
	if len(args) != 1 {
		return JSONBElements{}, fmt.Errorf("invalid JSONB elements arguments")
	}
	plan, err := sqltype.BindJSONBElements(e.Plan.Name(), args[0].ResultUsage(), info.Usage)
	return JSONBElements{info, plan, args}, err
}
func NewJSONBElements(info expr.Info, name string, arg expr.Expr) (JSONBElements, error) {
	plan, err := sqltype.BindJSONBElements(name, arg.ResultUsage(), info.Usage)
	if err != nil {
		return JSONBElements{}, err
	}
	return JSONBElements{info, plan, []expr.Expr{arg}}, nil
}
