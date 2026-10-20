package exec

import (
	"context"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// One state per actual call, reset at partition boundaries. A zero bucket means
// initialization has not succeeded; NULL inputs leave that state untouched.
// Counters use the full partition size, including any initial NULL results.
type WindowNtileState struct {
	Bucket                       int32
	Members, Boundary, Remainder int64
}

func (s *WindowNtileState) Initialize(total, buckets int64) error {
	if buckets <= 0 {
		return sqlError("22014", "argument of ntile must be greater than zero")
	}
	s.Bucket, s.Members = 1, 0
	s.Boundary = total / buckets
	s.Remainder = 0
	if s.Boundary == 0 {
		s.Boundary = 1
	} else {
		s.Remainder = total % buckets
		if s.Remainder != 0 {
			s.Boundary++
		}
	}
	return nil
}

func (s *WindowNtileState) Next() int64 {
	s.Members++
	if s.Members > s.Boundary {
		s.Bucket++
		if s.Remainder != 0 && int64(s.Bucket) == s.Remainder+1 {
			s.Remainder = 0
			s.Boundary--
		}
		s.Members = 1
	}
	return int64(s.Bucket)
}

func (s *WindowNtileState) evaluate(ctx context.Context, evaluation *Context, child *Stage, call plan.WindowCall, rows []int, current int) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	usage := call.Expression.ResultUsage()
	if s.Bucket == 0 {
		evaluation.BindQueryRow(child.Source, child.Index(rows[current]))
		value, err := call.Argument.Eval(evaluation)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := ValidateProducedValue(value, call.Argument.ResultUsage()); err != nil {
			return sqltype.Value{}, err
		}
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(usage.Type), nil
		}
		buckets, err := value.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := s.Initialize(int64(len(rows)), buckets); err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.Integer(usage.Type, s.Next())
}
