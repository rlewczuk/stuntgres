package engine

import (
	"context"

	"stuntgres/internal/sqltype"
)

// One state per actual call, reset at partition boundaries. A zero bucket means
// initialization has not succeeded; NULL inputs leave that state untouched.
// Counters use the full partition size, including any initial NULL results.
type windowNtileState struct {
	bucket                       int32
	members, boundary, remainder int64
}

func (s *windowNtileState) initialize(total, buckets int64) error {
	if buckets <= 0 {
		return sqlError("22014", "argument of ntile must be greater than zero")
	}
	s.bucket, s.members = 1, 0
	s.boundary = total / buckets
	s.remainder = 0
	if s.boundary == 0 {
		s.boundary = 1
	} else {
		s.remainder = total % buckets
		if s.remainder != 0 {
			s.boundary++
		}
	}
	return nil
}

func (s *windowNtileState) next() int64 {
	s.members++
	if s.members > s.boundary {
		s.bucket++
		if s.remainder != 0 && int64(s.bucket) == s.remainder+1 {
			s.remainder = 0
			s.boundary--
		}
		s.members = 1
	}
	return int64(s.bucket)
}

func (s *windowNtileState) evaluate(ctx context.Context, evaluation *EvalContext, child *indexedStage, call windowCall, rows []int, current int) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	usage := call.expression.ResultUsage()
	if s.bucket == 0 {
		evaluation.bindQueryRow(child.source, child.index(rows[current]))
		value, err := call.argument.Eval(evaluation)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := validateProducedValue(value, call.argument.ResultUsage()); err != nil {
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
		if err := s.initialize(int64(len(rows)), buckets); err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.Integer(usage.Type, s.next())
}
