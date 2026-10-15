package sqltype

import (
	"context"
	"math"
)

func (p StringPlan) evaluateSlice(ctx context.Context, texts [4]string, values [4]Value) (Value, error) {
	source := texts[0]
	var lo, hi int64
	toEnd := false
	if p.name == "substring" || p.name == "substr" {
		// Byte length bounds character indexes without a separate full count.
		var err error
		lo, hi, err = substringRange(len(source), values[1:p.count])
		if err != nil {
			return Value{}, err
		}
		// Omitted counts and clipped/overflowed ends already identify the byte
		// end. Do not traverse the retained suffix to rediscover that boundary.
		toEnd = p.count == 2 || hi == int64(len(source))
	} else {
		count := values[1].integer
		if count == 0 {
			return String(p.result.Type, ""), ctx.Err()
		}
		if count > 0 && count >= int64(len(source)) {
			return String(p.result.Type, source), ctx.Err()
		}
		if p.name == "left" && count > 0 {
			hi = count
		} else if p.name == "right" && count == math.MinInt32 {
			// Pinned PostgreSQL18.0 retains the source at this INT4 boundary.
			// Spell it explicitly rather than depending on signed overflow.
			return String(p.result.Type, source), ctx.Err()
		} else if p.name == "right" && count < 0 {
			// Only the omitted prefix is demanded; the suffix end is known.
			lo, hi, toEnd = min(-count, int64(len(source))), int64(len(source)), true
		} else {
			size, err := stringCharacters(ctx, source)
			if err != nil {
				return Value{}, err
			}
			hi = size
			if p.name == "left" {
				hi = max(size+count, 0)
			} else {
				lo, toEnd = max(size-count, 0), true
			}
		}
	}
	if lo == hi {
		return String(p.result.Type, ""), ctx.Err()
	}
	start, err := stringCharacterCut(ctx, source, lo)
	if err != nil {
		return Value{}, err
	}
	if toEnd {
		return String(p.result.Type, source[start:]), ctx.Err()
	}
	end, err := stringCharacterCut(ctx, source[start:], hi-lo)
	if err != nil {
		return Value{}, err
	}
	return String(p.result.Type, source[start:start+end]), nil
}
