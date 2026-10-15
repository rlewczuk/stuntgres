package functions

import (
	"fmt"

	"stuntgres/internal/sqltype"
)

type JSONRecordContext interface {
	Context
	FunctionInput() sqltype.InputEnvironment
}

type JSONRecord[E Expression[C], C JSONRecordContext, P any] struct {
	Info[P]
	Plan    sqltype.JSONRecordPlan
	Args    []E
	Defined bool
}

func (e JSONRecord[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Plan.ReturnsSet() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSON recordset call")
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return e.Plan.Evaluate(ctx, values[:len(e.Args)], c.FunctionInput())
}
func (e JSONRecord[E, C, P]) Reconstruct(info Info[P], args []E) (JSONRecord[E, C, P], error) {
	if len(args) < 1 || len(args) > 2 {
		return e, fmt.Errorf("invalid JSON record argument count")
	}
	var usages [2]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindJSONRecord(e.Plan.Name(), info.Usage, e.Plan.BaseIndex(), usages[:len(args)]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}
func NewJSONRecord[E Expression[C], C JSONRecordContext, P any](info Info[P], name string, baseIndex int, args []E) (JSONRecord[E, C, P], error) {
	var usages [2]sqltype.Usage
	if len(args) < 1 || len(args) > 2 {
		return JSONRecord[E, C, P]{}, fmt.Errorf("invalid JSON record argument count")
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindJSONRecord(name, info.Usage, baseIndex, usages[:len(args)]...)
	return JSONRecord[E, C, P]{Info: info, Plan: plan, Args: args}, err
}
