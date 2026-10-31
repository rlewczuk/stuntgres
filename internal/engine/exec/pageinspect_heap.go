package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) evalHeapFlagsPageinspect(n plan.InstalledPageinspectExpr) (sqltype.Value, error) {
	input, err := c.pageinspectInput(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	d := n.Usage.Type
	if input.null {
		return sqltype.Null(d), nil
	}
	first, err := input.values[0].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	second, err := input.values[1].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	flags := sqltype.DecodeHeapInfomaskFlags(int32(first), int32(second))
	fields := make([]sqltype.Value, 2)
	for i, names := range [][]string{flags.Raw[:flags.RawCount], flags.Combined[:flags.CombinedCount]} {
		a := d.CompositeMember(i).Usage.Type
		values := make([]sqltype.Value, len(names))
		for j, name := range names {
			values[j] = sqltype.String(a.Element(), name)
		}
		var dims []sqltype.ArrayDimension
		if len(values) != 0 {
			dims = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(values))}}
		}
		fields[i], err = sqltype.OwnedArrayValue(a, dims, values)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedCompositeValue(d, fields)
}
func (c *Context) openHeapPageinspect(n plan.InstalledPageinspectExpr) (expr.SourceCursor, error) {
	input, err := c.pageinspectInput(n)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	d := n.Usage.Type
	if input.null {
		return expr.OwnedSource(d, nil), nil
	}
	page, err := sqltype.ReadHeapPage(input.raw)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	rows := make([]sqltype.Value, page.Len())
	for i := range rows {
		item, err := page.Item(c.Context, i)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		fields, err := heapItemFields(d, item)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		if item.Tail {
			fields[13], err = sqltype.Binary(d.CompositeMember(13).Usage.Type, item.Data)
			if err != nil {
				return expr.SourceCursor{}, err
			}
		}
		rows[i], err = sqltype.OwnedCompositeValue(d, fields)
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	return expr.OwnedSource(d, rows), nil
}
