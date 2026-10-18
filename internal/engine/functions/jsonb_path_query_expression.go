package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// jsonbPathArguments evaluates supplied children once in written order, then
// the genuine omitted defaults. Strictness belongs to the typed consumer.
func jsonbPathArguments(c expr.Context, args [4]expr.Expr, count int, slots [4]uint8) ([4]sqltype.Value, error) {
	var values [4]sqltype.Value
	var supplied [4]bool
	for i := range count {
		slot := slots[i]
		value, err := args[slot].Eval(c)
		if err != nil {
			return values, err
		}
		values[slot], supplied[slot] = value, true
	}
	for i := range 4 {
		if supplied[i] {
			continue
		}
		value, err := args[i].Eval(c)
		if err != nil {
			return values, err
		}
		values[i] = value
	}
	return values, nil
}

// JSONBPathExists retains immutable executable formals and written presentation.
type JSONBPathExists struct {
	expr.Info
	Plan         sqltype.JSONBPathExistsPlan
	Args         [4]expr.Expr
	WrittenCount int
	WrittenSlots [4]uint8
	Labels       [4]string
	Variadic     bool
}

func (e *JSONBPathExists) Arguments() []expr.Expr { return e.Args[:] }
func (e JSONBPathExists) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	values, err := jsonbPathArguments(c, e.Args, e.WrittenCount, e.WrittenSlots)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Plan.Evaluate(ctx, values[:])
}
func (e JSONBPathExists) Reconstruct(info expr.Info, args [4]expr.Expr) (JSONBPathExists, error) {
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathExists(info.Usage, usages[:]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONBPathMatch retains immutable executable formals and written presentation.
type JSONBPathMatch struct {
	expr.Info
	Plan         sqltype.JSONBPathMatchPlan
	Args         [4]expr.Expr
	WrittenCount int
	WrittenSlots [4]uint8
	Labels       [4]string
	Variadic     bool
}

func (e *JSONBPathMatch) Arguments() []expr.Expr { return e.Args[:] }
func (e JSONBPathMatch) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	values, err := jsonbPathArguments(c, e.Args, e.WrittenCount, e.WrittenSlots)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Plan.Evaluate(ctx, values[:])
}
func (e JSONBPathMatch) Reconstruct(info expr.Info, args [4]expr.Expr) (JSONBPathMatch, error) {
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathMatch(info.Usage, usages[:]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONBPathQueryFirst retains immutable executable formals and written presentation.
type JSONBPathQueryFirst struct {
	expr.Info
	Plan         sqltype.JSONBPathQueryFirstPlan
	Args         [4]expr.Expr
	WrittenCount int
	WrittenSlots [4]uint8
	Labels       [4]string
	Variadic     bool
}

func (e *JSONBPathQueryFirst) Arguments() []expr.Expr { return e.Args[:] }
func (e JSONBPathQueryFirst) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	values, err := jsonbPathArguments(c, e.Args, e.WrittenCount, e.WrittenSlots)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Plan.Evaluate(ctx, values[:])
}
func (e JSONBPathQueryFirst) Reconstruct(info expr.Info, args [4]expr.Expr) (JSONBPathQueryFirst, error) {
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathQueryFirst(info.Usage, usages[:]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONBPathQuery retains immutable executable formals and written presentation.
type JSONBPathQuery struct {
	expr.Info
	Plan         sqltype.JSONBPathQueryPlan
	Args         [4]expr.Expr
	WrittenCount int
	WrittenSlots [4]uint8
	Labels       [4]string
	Variadic     bool
}

func (e *JSONBPathQuery) Arguments() []expr.Expr { return e.Args[:] }
func (e JSONBPathQuery) EvalSet(c expr.Context) ([]sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return nil, err
	}
	values, err := jsonbPathArguments(c, e.Args, e.WrittenCount, e.WrittenSlots)
	if err != nil {
		return nil, err
	}
	return e.Plan.Evaluate(ctx, values[:])
}
func (e JSONBPathQuery) Reconstruct(info expr.Info, args [4]expr.Expr) (JSONBPathQuery, error) {
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathQuery(info.Usage, usages[:]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

func (e JSONBPathQuery) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSONPath query set call")
}
