package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type statisticsMCVItemsState struct {
	view   sqltype.StatisticsMCVView
	output sqltype.OutputEnvironment
}

func (StatisticsMCVItems) SourceAdmission() (bool, bool) { return true, true }
func (n StatisticsMCVItems) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(StatisticsMCVItems)
	return ok && sqltype.SameRecordUsage(n.Usage, o.Usage)
}
func (n StatisticsMCVItems) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if err := n.ValidateStoredProvider(); err != nil {
		return expr.SourceCursor{}, err
	}
	value, err := n.Arg.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if err := ctx.Err(); err != nil {
		return expr.SourceCursor{}, err
	}
	if !value.ValidResult(n.Arg.ResultUsage()) {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid typed MCV item input")
	}
	if value.IsNull() {
		return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, Exhausted: true}), nil
	}
	view, err := value.StatisticsMCV()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	output := c.FunctionStringOutput()
	// MCV's physical producer admits no XML dictionaries. Do not retain an
	// unrelated live warning callback in the invocation's presentation snapshot.
	output.XMLNotice = nil
	return expr.NewSourceCursor(expr.SourceCursor{
		Type: n.Usage.Type, Value: value,
		State:   &statisticsMCVItemsState{view: view, output: output},
		Advance: nextStatisticsMCVItems,
	}), nil
}

func nextStatisticsMCVItems(ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	state := cursor.State.(*statisticsMCVItemsState)
	if cursor.Position == state.view.Len() {
		return sqltype.Value{}, false, nil
	}
	item, err := state.view.Item(cursor.Position)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	textArray := cursor.Type.CompositeMember(1).Usage.Type
	boolArray := cursor.Type.CompositeMember(2).Usage.Type
	width := state.view.Width()
	values := make([]sqltype.Value, width)
	nulls := make([]sqltype.Value, width)
	for dimension := range width {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, false, err
		}
		value, err := state.view.Value(cursor.Position, dimension)
		if err != nil {
			return sqltype.Value{}, false, err
		}
		values[dimension] = sqltype.Null(textArray.Element())
		if !value.IsNull() {
			text, err := value.Type().OutputWithEnvironment(ctx, value, state.output)
			if err != nil {
				return sqltype.Value{}, false, err
			}
			values[dimension] = sqltype.String(textArray.Element(), text)
		}
		nulls[dimension] = sqltype.Boolean(boolArray.Element(), item.Nulls[dimension])
	}
	fields := make([]sqltype.Value, 5)
	fields[0], err = sqltype.Integer(cursor.Type.CompositeMember(0).Usage.Type, int64(cursor.Position))
	if err != nil {
		return sqltype.Value{}, false, err
	}
	fields[1], err = sqltype.OwnedArrayValue(textArray, []sqltype.ArrayDimension{{Lower: 1, Length: int32(width)}}, values)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	fields[2], err = sqltype.OwnedArrayValue(boolArray, []sqltype.ArrayDimension{{Lower: 1, Length: int32(width)}}, nulls)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	fields[3], err = sqltype.Float(cursor.Type.CompositeMember(3).Usage.Type, item.Frequency)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	fields[4], err = sqltype.Float(cursor.Type.CompositeMember(4).Usage.Type, item.BaseFrequency)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	row, err := sqltype.OwnedCompositeValue(cursor.Type, fields)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, false, err
	}
	cursor.Position++
	return row, true, nil
}
