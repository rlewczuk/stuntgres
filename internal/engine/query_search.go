package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// searchRecipe is immutable analysis state. Actual ancestry comes exclusively
// from the joined worktable row; evaluation order is deliberately unchanged.
type searchRecipe struct {
	Syntax         *parser.SearchClause
	ShapeError     error
	Slot           int
	Keys           []int
	Record, Result *sqltype.Descriptor
	Depth          *sqltype.Descriptor
	Grouping       []sqltype.Grouping // DISTINCT only, in record-field order.
}

func (s *searchRecipe) Value(ctx context.Context, row []sqltype.Value, parent sqltype.Value) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	offset := 0
	if s.Syntax.Breadth {
		offset = 1
	}
	fields := make([]sqltype.Value, len(s.Keys)+offset)
	if offset != 0 {
		depth := int64(0)
		if parent.Type() != nil {
			if parent.Type() != s.Result || parent.IsNull() {
				return sqltype.Value{}, fmt.Errorf("stuntgres: invalid search parent")
			}
			n, err := parent.CompositeField(0).Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
			depth = n + 1
		}
		var err error
		fields[0], err = sqltype.Integer(s.Depth, depth)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for i, key := range s.Keys {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		fields[i+offset] = row[key]
	}
	record, err := sqltype.OwnedCompositeValue(s.Record, fields)
	if err != nil || s.Syntax.Breadth {
		return record, err
	}
	length := 0
	if parent.Type() != nil {
		if parent.Type() != s.Result || parent.IsNull() {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid search parent")
		}
		length = parent.ArrayLen()
	}
	elements := make([]sqltype.Value, length+1)
	for i := range length {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		elements[i] = parent.ArrayElement(i)
	}
	elements[length] = record
	return sqltype.OwnedArrayValue(s.Result, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
}

func (s *searchRecipe) Equal(ctx context.Context, a, b sqltype.Value) (bool, error) {
	c := cycleRecipe{Record: s.Record, Path: s.Result, Grouping: s.Grouping}
	if s.Syntax.Breadth {
		return c.equalRecord(ctx, a, b)
	}
	return c.EqualPath(ctx, a, b)
}
func (s *searchRecipe) Hash(ctx context.Context, value sqltype.Value) (uint64, error) {
	c := cycleRecipe{Record: s.Record, Path: s.Result, Grouping: s.Grouping}
	if !s.Syntax.Breadth {
		return c.HashPath(ctx, value)
	}
	if value.Type() != s.Result || value.IsNull() {
		return 0, fmt.Errorf("stuntgres: foreign search record")
	}
	hash := uint64(14695981039346656037)
	for i, g := range s.Grouping {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		part, err := g.Hash(ctx, value.CompositeField(i))
		if err != nil {
			return 0, err
		}
		hash = (hash ^ part) * 1099511628211
	}
	return hash, ctx.Err()
}
