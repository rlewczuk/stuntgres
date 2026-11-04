package exec

import (
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) evalHeapFlagsPageinspect(n plan.InstalledPageinspectExpr) (sqltype.Value, error) {
	input, err := c.pageinspectInput(n)
	defer input.token.Leave()
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
