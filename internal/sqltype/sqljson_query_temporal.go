package sqltype

import (
	"context"
	"fmt"
)

func sqlJSONTemporal(id ID) bool {
	return id == Date || id == Time || id == Timetz || id == Timestamp || id == Timestamptz
}

func compareSQLJSONTemporal(ctx context.Context, a, b Value, input InputEnvironment) (int, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	aid, bid := a.typ.ID(), b.typ.ID()
	if dateTimestampType(aid) && dateTimestampType(bid) {
		if aid != bid {
			return compareDatetime(a, b, input), true, nil
		}
	} else if (aid == Time || aid == Timetz) && (bid == Time || bid == Timetz) {
		if aid != bid {
			if input.TransactionStart.IsZero() && !input.Zone.isFixed() {
				return 0, false, fmt.Errorf("missing temporal comparison clock")
			}
			east, _ := input.Zone.OffsetAt(input.TransactionStart.Unix())
			day := floorDiv(input.TransactionStart.Unix()+int64(east), 86400)
			if aid == Time {
				a.west = -input.Zone.CivilOffset(day*86400 + a.integer/1000000)
				a.typ = b.typ
			} else {
				b.west = -input.Zone.CivilOffset(day*86400 + b.integer/1000000)
				b.typ = a.typ
			}
		}
	} else {
		return 0, false, nil
	}
	comparison, err := BindComparison(a.typ, b.typ, CollationRef{}, false)
	if err != nil {
		return 0, false, err
	}
	result, err := comparison.Compare(ctx, a, b, input)
	return result, true, err
}

// SQL/JSON datetime items retain tz=0 for timestamptz JSON encoding. SQL VALUE
// output instead calls the original type's output provider with the live zone.
func sqlJSONTemporalJSON(v Value) string {
	if v.typ.ID() != Time && v.typ.ID() != Timetz {
		return jsonCreateDateTime(v, OutputEnvironment{})
	}
	text := timeOutput(v.integer)
	if v.typ.ID() == Time {
		return text
	}
	east := -v.west
	sign := byte('+')
	if east < 0 {
		sign = '-'
		east = -east
	}
	var scratch [16]byte
	suffix := append(scratch[:0], sign)
	suffix = jsonDateTimeDigits(suffix, int64(east/3600), 2)
	suffix = append(suffix, ':')
	suffix = jsonDateTimeDigits(suffix, int64(east/60%60), 2)
	if east%60 != 0 {
		suffix = append(suffix, ':')
		suffix = jsonDateTimeDigits(suffix, int64(east%60), 2)
	}
	return text + string(suffix)
}
