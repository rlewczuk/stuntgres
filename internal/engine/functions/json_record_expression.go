package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type JSONRecord struct {
	expr.Info
	Plan    sqltype.JSONRecordPlan
	Args    []expr.Expr
	Defined bool
}

func (e JSONRecord) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e JSONRecord) Reconstruct(info expr.Info, args []expr.Expr) (JSONRecord, error) {
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
func NewJSONRecord(info expr.Info, name string, baseIndex int, args []expr.Expr) (JSONRecord, error) {
	var usages [2]sqltype.Usage
	if len(args) < 1 || len(args) > 2 {
		return JSONRecord{}, fmt.Errorf("invalid JSON record argument count")
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindJSONRecord(name, info.Usage, baseIndex, usages[:len(args)]...)
	return JSONRecord{Info: info, Plan: plan, Args: args}, err
}
