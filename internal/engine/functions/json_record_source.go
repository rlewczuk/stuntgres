package functions

import (
	"context"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e JSONRecord) SourceAdmission() (bool, bool) { return true, e.Plan.ReturnsSet() }

func (e JSONRecord) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(JSONRecord)
	return ok && e.Plan.Name() == o.Plan.Name() && e.Plan.BaseIndex() == o.Plan.BaseIndex() &&
		e.Defined == o.Defined && sqltype.SameRecordUsage(e.ResultUsage(), o.ResultUsage())
}

func (e JSONRecord) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if !e.Plan.ReturnsSet() {
		value, err := e.Eval(c)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(value), nil
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	cursor, err := e.Plan.Open(ctx, values[:len(e.Args)], c.FunctionInput())
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.NewSourceCursor(expr.SourceCursor{
		Type: e.Usage.Type, State: &cursor,
		Advance: nextJSONRecordSource, Release: closeJSONRecordSource,
	}), nil
}

func nextJSONRecordSource(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	return s.State.(*sqltype.JSONRecordCursor).Next(ctx)
}

func closeJSONRecordSource(state any) {
	state.(*sqltype.JSONRecordCursor).Close()
}
