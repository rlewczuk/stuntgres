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
	first, err := input.first.Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	second, err := input.second.Integer()
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
		fields := make([]sqltype.Value, 14)
		for j := range fields {
			fields[j] = sqltype.Null(d.CompositeMember(j).Usage.Type)
		}
		for j, number := range [...]int64{int64(item.LP), int64(item.Offset), int64(item.Flags), int64(item.Length)} {
			fields[j], err = sqltype.Integer(d.CompositeMember(j).Usage.Type, number)
			if err != nil {
				return expr.SourceCursor{}, err
			}
		}
		if item.Header {
			fields[4], err = sqltype.XIDValue(d.CompositeMember(4).Usage.Type, item.Xmin)
			if err != nil {
				return expr.SourceCursor{}, err
			}
			fields[5], err = sqltype.XIDValue(d.CompositeMember(5).Usage.Type, item.Xmax)
			if err != nil {
				return expr.SourceCursor{}, err
			}
			fields[6], err = sqltype.Integer(d.CompositeMember(6).Usage.Type, int64(item.Field3))
			if err != nil {
				return expr.SourceCursor{}, err
			}
			fields[7], err = sqltype.TIDValue(d.CompositeMember(7).Usage.Type, item.CTIDBlock, item.CTIDOffset)
			if err != nil {
				return expr.SourceCursor{}, err
			}
			for j, number := range [...]int64{int64(item.Infomask2), int64(item.Infomask), int64(item.Hoff)} {
				fields[j+8], err = sqltype.Integer(d.CompositeMember(j+8).Usage.Type, number)
				if err != nil {
					return expr.SourceCursor{}, err
				}
			}
		}
		if item.Tail {
			if item.HasBits {
				fields[11] = sqltype.String(d.CompositeMember(11).Usage.Type, item.Bits)
			}
			if item.HasOID {
				fields[12], err = sqltype.OIDValue(d.CompositeMember(12).Usage.Type, item.OID)
				if err != nil {
					return expr.SourceCursor{}, err
				}
			}
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
