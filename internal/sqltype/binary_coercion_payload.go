package sqltype

import (
	"context"
	"unicode/utf8"
)

// Validate only the native text provider's invariants, not target input syntax.
// This deliberately admits non-JSON text as a raw JSON datum. Poll within long
// inputs without a temporary byte slice or normalized/replacement string.
func binaryTextValid(ctx context.Context, text string) error {
	nextPoll := 0
	for i := 0; i < len(text); {
		if i >= nextPoll {
			if err := ctx.Err(); err != nil {
				return err
			}
			nextPoll = i + 4096
		}
		if text[i] == 0 {
			return binaryValueUnsupported()
		}
		if text[i] < utf8.RuneSelf {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			return binaryValueUnsupported()
		}
		i += size
	}
	return ctx.Err()
}

func binaryGeometryTriple(v Value, target *Descriptor) (Value, error) {
	var triple [4]float64
	if v.typ.id == Line {
		if v.geometry == nil {
			return Value{}, binaryValueUnsupported()
		}
		triple = v.geometry.coordinates
	} else {
		triple[0], triple[1], triple[2] = v.circle.x, v.circle.y, v.circle.radius
	}
	if target.id == Circle {
		return Value{typ: target, circle: circleDatum{triple[0], triple[1], triple[2]}}, nil
	}
	if boxFuzzyEqual(triple[0], 0) && boxFuzzyEqual(triple[1], 0) {
		return Value{}, binaryValueUnsupported()
	}
	if v.typ.id == Line {
		return Value{typ: target, geometry: v.geometry}, nil
	}
	return Value{typ: target, geometry: &geometryPayload{coordinates: triple}}, nil
}
