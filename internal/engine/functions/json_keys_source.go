package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e JSONKeys) SourceAdmission() (bool, bool) { return true, true }

func (e JSONKeys) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(JSONKeys)
	return ok && e.Plan.Name() == o.Plan.Name()
}

func (e JSONKeys) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if len(e.Args) != 1 {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid JSON keys arguments")
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
		Advance: nextJSONKeysSource, Release: closeJSONKeysSource,
	}), nil
}

func nextJSONKeysSource(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	return s.State.(*sqltype.JSONKeyCursor).Next(ctx)
}

func closeJSONKeysSource(state any) {
	state.(*sqltype.JSONKeyCursor).Close()
}
