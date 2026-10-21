package plan

import (
	"context"
	"fmt"
	"math"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e SeriesExpr) SourceAdmission() (bool, bool) { return true, true }

func (e SeriesExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(SeriesExpr)
	return ok && e.ResultUsage() == o.ResultUsage() && len(e.Args) == len(o.Args)
}

func (e SeriesExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	if len(e.Args) < 2 || len(e.Args) > 4 || len(e.Args) == 4 && e.Usage.Type.ID() != sqltype.Timestamptz {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid series arguments")
	}
	if e.Usage.Type.ID() == sqltype.Timestamp || e.Usage.Type.ID() == sqltype.Timestamptz {
		if len(e.Args) < 3 {
			return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid timestamp series arguments")
		}
		var values [4]sqltype.Value
		for i, arg := range e.Args {
			value, err := arg.Eval(ctx)
			if err != nil {
				return expr.SourceCursor{}, err
			}
			values[i] = value
		}
		var cursor sqltype.TimestampSeriesCursor
		var err error
		if e.Usage.Type.ID() == sqltype.Timestamptz {
			cursor, err = sqltype.OpenTimestamptzSeries(ctx.FunctionContext(), values[0], values[1], values[2], ctx.FunctionInput(), values[3:len(e.Args)]...)
		} else {
			cursor, err = sqltype.OpenTimestampSeries(ctx.FunctionContext(), values[0], values[1], values[2])
		}
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.NewSourceCursor(expr.SourceCursor{
			Type: e.Usage.Type, State: &cursor,
			Advance: nextTimestampSeriesSource, Release: closeTimestampSeriesSource,
		}), nil
	}
	if e.Usage.Type.ID() == sqltype.Numeric {
		var values [3]sqltype.Value
		for i, arg := range e.Args {
			value, err := arg.Eval(ctx)
			if err != nil {
				return expr.SourceCursor{}, err
			}
			values[i] = value
		}
		cursor, err := sqltype.OpenNumericSeries(ctx.FunctionContext(), values[0], values[1], values[2:len(e.Args)]...)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.NewSourceCursor(expr.SourceCursor{
			Type: e.Usage.Type, State: &cursor,
			Advance: nextNumericSeriesSource, Release: closeNumericSeriesSource,
		}), nil
	}
	var values [3]int64
	values[2] = 1
	null := false
	for i, arg := range e.Args {
		v, err := arg.Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		if v.IsNull() {
			null = true
		} else {
			values[i], err = v.Integer()
			if err != nil {
				return expr.SourceCursor{}, err
			}
		}
	}
	cursor := expr.NewSourceCursor(expr.SourceCursor{
		Type: e.Usage.Type, Current: values[0], Stop: values[1], Step: values[2],
		Exhausted: null, Advance: nextIntegerSource,
	})
	if null {
		return cursor, nil
	}
	if cursor.Step == 0 {
		return expr.SourceCursor{}, sqlError("22023", "step size cannot equal zero")
	}
	cursor.Exhausted = cursor.Step > 0 && cursor.Current > cursor.Stop || cursor.Step < 0 && cursor.Current < cursor.Stop
	return cursor, nil
}

func nextNumericSeriesSource(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	return s.State.(*sqltype.NumericSeriesCursor).Next(ctx)
}

func closeNumericSeriesSource(state any) {
	state.(*sqltype.NumericSeriesCursor).Close()
}

func nextTimestampSeriesSource(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	return s.State.(*sqltype.TimestampSeriesCursor).Next(ctx)
}

func closeTimestampSeriesSource(state any) {
	state.(*sqltype.TimestampSeriesCursor).Close()
}

// Series and subscripts share checked arithmetic, not a provider discriminator.
func nextIntegerSource(_ context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	if s.Step > 0 && s.Current > s.Stop || s.Step < 0 && s.Current < s.Stop {
		return sqltype.Value{}, false, nil
	}
	value, err := sqltype.Integer(s.Type, s.Current)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	minimum, maximum := int64(math.MinInt64), int64(math.MaxInt64)
	if s.Type.ID() == sqltype.Int4 {
		minimum, maximum = math.MinInt32, math.MaxInt32
	}
	if s.Step > 0 && s.Current > maximum-s.Step || s.Step < 0 && s.Current < minimum-s.Step {
		s.Exhausted = true
	} else {
		s.Current += s.Step
	}
	return value, true, nil
}
