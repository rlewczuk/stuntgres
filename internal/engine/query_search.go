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
	syntax         *parser.SearchClause
	shapeError     error
	slot           int
	keys           []int
	record, result *sqltype.Descriptor
	depth          *sqltype.Descriptor
	grouping       []sqltype.Grouping // DISTINCT only, in record-field order.
}

func (s *searchRecipe) value(ctx context.Context, row []sqltype.Value, parent sqltype.Value) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	offset := 0
	if s.syntax.Breadth {
		offset = 1
	}
	fields := make([]sqltype.Value, len(s.keys)+offset)
	if offset != 0 {
		depth := int64(0)
		if parent.Type() != nil {
			if parent.Type() != s.result || parent.IsNull() {
				return sqltype.Value{}, fmt.Errorf("stuntgres: invalid search parent")
			}
			n, err := parent.CompositeField(0).Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
			depth = n + 1
		}
		var err error
		fields[0], err = sqltype.Integer(s.depth, depth)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for i, key := range s.keys {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		fields[i+offset] = row[key]
	}
	record, err := sqltype.OwnedCompositeValue(s.record, fields)
	if err != nil || s.syntax.Breadth {
		return record, err
	}
	length := 0
	if parent.Type() != nil {
		if parent.Type() != s.result || parent.IsNull() {
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
	return sqltype.OwnedArrayValue(s.result, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
}

func (s *searchRecipe) equal(ctx context.Context, a, b sqltype.Value) (bool, error) {
	c := cycleRecipe{record: s.record, path: s.result, grouping: s.grouping}
	if s.syntax.Breadth {
		return c.equalRecord(ctx, a, b)
	}
	return c.equalPath(ctx, a, b)
}
func (s *searchRecipe) hash(ctx context.Context, value sqltype.Value) (uint64, error) {
	c := cycleRecipe{record: s.record, path: s.result, grouping: s.grouping}
	if !s.syntax.Breadth {
		return c.hashPath(ctx, value)
	}
	if value.Type() != s.result || value.IsNull() {
		return 0, fmt.Errorf("stuntgres: foreign search record")
	}
	hash := uint64(14695981039346656037)
	for i, g := range s.grouping {
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
