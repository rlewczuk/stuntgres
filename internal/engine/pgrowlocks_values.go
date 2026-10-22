package engine

import (
	"context"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

func pgrowlocksMode(mode lock.MultiMode) string {
	switch mode {
	case lock.MultiKeyShare:
		return "For Key Share"
	case lock.MultiShare:
		return "For Share"
	case lock.MultiNoKeyLock:
		return "For No Key Update"
	case lock.MultiUpdateLock:
		return "For Update"
	case lock.MultiNoKeyAction:
		return "No Key Update"
	case lock.MultiUpdateAction:
		return "Update"
	}
	return ""
}
func pgrowlocksValues(ctx context.Context, result *sqltype.Descriptor, records []lock.PgrowlocksRecord) ([]sqltype.Value, error) {
	rows := make([]sqltype.Value, len(records))
	for i, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		block, offset, err := pgrowlocksLogicalTID(record.Tuple.Version)
		if err != nil {
			return nil, err
		}
		fields := make([]sqltype.Value, 6)
		fields[0], err = sqltype.TIDValue(result.CompositeMember(0).Usage.Type, block, offset)
		if err != nil {
			return nil, err
		}
		fields[1], err = sqltype.XIDValue(result.CompositeMember(1).Usage.Type, record.Locker)
		if err != nil {
			return nil, err
		}
		fields[2] = sqltype.Boolean(result.CompositeMember(2).Usage.Type, record.Multi)
		for j := 3; j < 6; j++ {
			array := result.CompositeMember(j).Usage.Type
			values := make([]sqltype.Value, len(record.Members))
			for k, member := range record.Members {
				switch j {
				case 3:
					values[k], err = sqltype.XIDValue(array.Element(), member.XID)
				case 4:
					values[k] = sqltype.String(array.Element(), pgrowlocksMode(member.Mode))
				case 5:
					values[k], err = sqltype.Integer(array.Element(), int64(member.PID))
				}
				if err != nil {
					return nil, err
				}
			}
			fields[j], err = sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Length: int32(len(values)), Lower: 1}}, values)
			if err != nil {
				return nil, err
			}
		}
		rows[i], err = sqltype.OwnedCompositeValue(result, fields)
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
