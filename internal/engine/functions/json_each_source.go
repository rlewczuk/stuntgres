package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e JSONEach) SourceAdmission() (bool, bool) { return true, true }

func (e JSONEach) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(JSONEach)
	return ok && e.Plan.Name() == o.Plan.Name() && sqltype.SameRecordUsage(e.ResultUsage(), o.ResultUsage())
}

func (e JSONEach) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if len(e.Args) != 1 {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid JSON each arguments")
	}
	value, err := e.Args[0].Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	cursor, err := e.Plan.Open(ctx, value)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.NewSourceCursor(expr.SourceCursor{
		Type: e.Usage.Type, State: &cursor,
		Advance: nextJSONEachSource, Release: closeJSONEachSource,
	}), nil
}

func nextJSONEachSource(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	return s.State.(*sqltype.JSONEachCursor).Next(ctx)
}

func closeJSONEachSource(state any) {
	state.(*sqltype.JSONEachCursor).Close()
}
