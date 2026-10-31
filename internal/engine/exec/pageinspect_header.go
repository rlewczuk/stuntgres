package exec

import (
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) evalHeaderPageinspect(n plan.InstalledPageinspectExpr) (sqltype.Value, error) {
	input, err := c.pageinspectInput(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	d := n.Usage.Type
	if input.null {
		return sqltype.Null(d), nil
	}
	if n.Member == "page_checksum" {
		block, err := input.values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		checksum, isNew, err := sqltype.ReadPageChecksum(input.raw, block)
		if err != nil {
			return sqltype.Value{}, err
		}
		if isNew {
			return sqltype.Null(d), nil
		}
		return sqltype.Integer(d, int64(checksum))
	}
	header, err := sqltype.ReadPageHeader(input.raw)
	if err != nil {
		return sqltype.Value{}, err
	}
	fields := make([]sqltype.Value, 9)
	fields[0], err = sqltype.PGLSNValue(d.CompositeMember(0).Usage.Type, header.LSN)
	if err != nil {
		return sqltype.Value{}, err
	}
	for i, number := range [...]int64{int64(header.Checksum), int64(header.Flags), int64(header.Lower), int64(header.Upper), int64(header.Special), int64(header.PageSize), int64(header.Version)} {
		fields[i+1], err = sqltype.Integer(d.CompositeMember(i+1).Usage.Type, number)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	fields[8], err = sqltype.XIDValue(d.CompositeMember(8).Usage.Type, header.PruneXID)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.OwnedCompositeValue(d, fields)
}
