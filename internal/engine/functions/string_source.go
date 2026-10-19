package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e String) SourceAdmission() (bool, bool) {
	set := e.Plan.Name() == "string_to_table"
	return set, set
}

func (e String) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(String)
	return ok && e.Plan.Name() == "string_to_table" && o.Plan.Name() == "string_to_table"
}

func (e String) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if e.Plan.Name() != "string_to_table" {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
	}
	var values [4]sqltype.Value
	for i, arg := range e.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	cursor, err := e.Plan.OpenStringTable(ctx, values, c.FunctionInput(), c.FunctionStringOutput())
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.NewSourceCursor(expr.SourceCursor{
		Type: e.Usage.Type, State: &cursor,
		Advance: nextStringSource, Release: closeStringSource,
	}), nil
}

func nextStringSource(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	return s.State.(*sqltype.StringFieldCursor).Next(ctx)
}

func closeStringSource(state any) {
	state.(*sqltype.StringFieldCursor).Close()
}
