package exec

import (
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) evalHashPageinspect(n plan.InstalledPageinspectExpr) (sqltype.Value, error) {
	input, err := c.pageinspectInput(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	d := n.Usage.Type
	defer input.token.Leave()
	if input.null {
		return sqltype.Null(d), nil
	}
	kind := sqltype.HashAnyPage
	if n.Member == "hash_page_stats" {
		kind = sqltype.HashBucketPage
	}
	if n.Member == "hash_metapage_info" {
		kind = sqltype.HashMetaPage
	}
	page, err := sqltype.ReadHashPage(input.raw, kind)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.Member == "hash_page_type" {
		return sqltype.String(d, page.PageType()), nil
	}
	fields := make([]sqltype.Value, d.CompositeLen())
	if n.Member == "hash_page_stats" {
		stats, failure := page.Stats()
		if failure != nil {
			return sqltype.Value{}, failure
		}
		for i, v := range stats {
			fields[i], err = sqltype.Integer(d.CompositeMember(i).Usage.Type, v)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
	} else {
		for i := range 14 {
			typ := d.CompositeMember(i).Usage.Type
			switch i {
			case 2:
				fields[i], err = sqltype.Float(typ, page.MetaTuples())
			case 13:
				fields[i], err = sqltype.OIDValue(typ, uint32(page.MetaInteger(i)))
			default:
				fields[i], err = sqltype.Integer(typ, page.MetaInteger(i))
			}
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		for array, length := range []int{98, 1024} {
			typ := d.CompositeMember(14 + array).Usage.Type
			values := make([]sqltype.Value, length)
			for j := range length {
				values[j], err = sqltype.Integer(typ.Element(), int64(page.MetaArrayElement(array, j)))
				if err != nil {
					return sqltype.Value{}, err
				}
			}
			fields[14+array], err = sqltype.OwnedArrayValue(typ, []sqltype.ArrayDimension{{Lower: 1, Length: int32(length)}}, values)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
	}
	return sqltype.OwnedCompositeValue(d, fields)
}
func hashPageItemValue(d *sqltype.Descriptor, item sqltype.HashPageItem) (sqltype.Value, error) {
	fields := make([]sqltype.Value, 3)
	var err error
	fields[0], err = sqltype.Integer(d.CompositeMember(0).Usage.Type, int64(item.Offset))
	if err != nil {
		return sqltype.Value{}, err
	}
	fields[1], err = sqltype.TIDValue(d.CompositeMember(1).Usage.Type, item.TID.Block, item.TID.Offset)
	if err != nil {
		return sqltype.Value{}, err
	}
	fields[2], err = sqltype.Integer(d.CompositeMember(2).Usage.Type, int64(item.Data))
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.OwnedCompositeValue(d, fields)
}
