package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// StatisticExtOID identifies the extended-statistics definitions catalog.
const StatisticExtOID ids.OID = 3381

func capturePGStatisticExt(in Input) ([][]sqltype.Value, error) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: StatisticExtOID}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner ||
		!RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address ||
		in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() ||
		in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid statistics definition source")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, raw := range in.Catalog.Descriptors() {
		d, ok := raw.(*catalog.Statistics)
		if !ok {
			continue
		}
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.StatisticsProvider{}).Validate(in.Catalog, d); err != nil {
			return nil, err
		}
		if _, err := in.Roles.Descriptor(d.Owner); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 9)
		for i, oid := range []ids.OID{d.Address().ObjectID, d.Relation.Address.ObjectID, 0, d.Namespace().ObjectID, ids.OID(d.Owner.ID())} {
			if i == 2 {
				continue
			}
			v, err := sqltype.OIDValue(in.Source.Columns[i].Usage.Type, uint32(oid))
			if err != nil {
				return nil, err
			}
			row[i] = v
		}
		row[2] = sqltype.String(in.Source.Columns[2].Usage.Type, d.Name())
		keys := make([]int16, d.Selection.Len())
		for i := range keys {
			keys[i] = int16(d.Selection.Key(i))
		}
		var err error
		row[5], err = sqltype.CatalogInt2Vector(in.Source.Columns[5].Usage.Type, keys)
		if err != nil {
			return nil, err
		}
		row[6] = sqltype.Null(in.Source.Columns[6].Usage.Type)
		if d.TargetSet {
			row[6], err = sqltype.Integer(in.Source.Columns[6].Usage.Type, int64(d.Target))
			if err != nil {
				return nil, err
			}
		}
		kinds := d.Selection.Kinds()
		items := make([]sqltype.Value, len(kinds))
		for i := range kinds {
			items[i] = sqltype.String(in.Source.Columns[7].Usage.Type.Element(), kinds[i:i+1])
		}
		row[7], err = sqltype.OwnedArrayValue(in.Source.Columns[7].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(items))}}, items)
		if err != nil {
			return nil, err
		}
		row[8] = sqltype.Null(in.Source.Columns[8].Usage.Type)
		if d.Expressions.Len() != 0 {
			expressions := make([]expr.Expr, d.Expressions.Len())
			for i := range expressions {
				expressions[i] = d.Expressions.At(i).Definition()
			}
			row[8], err = sqltype.Tree(in.Source.Columns[8].Usage.Type, plan.OwnExpressionListTree(expressions))
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
