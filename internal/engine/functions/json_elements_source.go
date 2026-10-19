package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e JSONElements) SourceAdmission() (bool, bool) { return true, true }

func (e JSONElements) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(JSONElements)
	return ok && e.Plan.Name() == o.Plan.Name() && e.ResultUsage() == o.ResultUsage()
}

func (e JSONElements) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if len(e.Args) != 1 {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid JSON elements arguments")
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
		Advance: nextJSONElementsSource, Release: closeJSONElementsSource,
	}), nil
}

func nextJSONElementsSource(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	return s.State.(*sqltype.JSONElementsCursor).Next(ctx)
}

func closeJSONElementsSource(state any) {
	state.(*sqltype.JSONElementsCursor).Close()
}

func (e JSONElements) ScalarSourceOutName() string { return "value" }
