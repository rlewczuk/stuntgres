package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e JSONBElements) SourceAdmission() (bool, bool) { return true, true }

func (e JSONBElements) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(JSONBElements)
	return ok && e.Plan.Name() == o.Plan.Name() && e.ResultUsage() == o.ResultUsage()
}

func (e JSONBElements) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if len(e.Args) != 1 {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid JSONB elements arguments")
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
		Advance: nextJSONBElementsSource, Release: closeJSONBElementsSource,
	}), nil
}

func nextJSONBElementsSource(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	return s.State.(*sqltype.JSONBElementCursor).Next(ctx)
}

func closeJSONBElementsSource(state any) {
	state.(*sqltype.JSONBElementCursor).Close()
}

func (e JSONBElements) ScalarSourceOutName() string { return "value" }
