package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e unnestExpression) SourceAdmission() (bool, bool) { return true, true }

func (e unnestExpression) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(unnestExpression)
	return ok && SameIntaggIdentity(e.Procedure, e.Ref, o.Procedure, o.Ref) && e.ResultUsage() == o.ResultUsage()
}

func (e unnestExpression) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	value, err := e.Args[0].Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if input := e.Args[0].ResultUsage().Type; input.BaseType().IsArray() {
		if value.Type() != input || input.BaseType().Element() != e.Usage.Type {
			return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid array unnest generation")
		}
		return expr.NewSourceCursor(expr.SourceCursor{
			Type: e.Usage.Type, Value: value,
			Exhausted: value.IsNull() || value.ArrayLen() == 0, Advance: nextArraySource,
		}), nil
	}
	cursor, err := sqltype.OpenMultirange(ctx.FunctionContext(), e.Args[0].ResultUsage(), e.ResultUsage(), value)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.NewSourceCursor(expr.SourceCursor{
		Type: e.Usage.Type, State: &cursor,
		Advance: nextMultirangeSource, Release: closeMultirangeSource,
	}), nil
}

func nextArraySource(_ context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	if s.Position == s.Value.ArrayLen() {
		return sqltype.Value{}, false, nil
	}
	value := s.Value.ArrayElement(s.Position)
	if value.Type() != s.Type {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: invalid array unnest element generation")
	}
	s.Position++
	return value, true, nil
}

func nextMultirangeSource(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	return s.State.(*sqltype.MultirangeCursor).Next(ctx)
}

func closeMultirangeSource(state any) {
	state.(*sqltype.MultirangeCursor).Close()
}
