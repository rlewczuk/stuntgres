package sqltype

import (
	"context"
	"math"
	"slices"
)

// SamePayload compares stored datum representation, not SQL equality or descriptor
// ownership. Replacement uses it to distinguish a retag from a changed CHECK input.
// Immutable array/composite containers may have been reowned without changing any
// cell. No datum is rendered, coerced, copied, or evaluated by this comparison.
func (v Value) SamePayload(ctx context.Context, other Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if v.null || other.null {
		return v.null == other.null, nil
	}
	if v.typ != nil && other.typ != nil && v.typ.BaseType().id == Regclass && other.typ.BaseType().id == Regclass && v.integer == 1 && other.integer == 1 {
		// Canonical decimal text is optional for inline numeric identities.
		v.text, other.text = "", ""
	}
	if v.typ != nil && other.typ != nil && v.typ.BaseType().id == Float4 && other.typ.BaseType().id == Float4 {
		if v.float4Bits() != other.float4Bits() {
			return false, nil
		}
		// A raw word and an ordinary computed value with that word are the
		// same payload, regardless of sidecar provenance or widened NaN bits.
		v.floating, other.floating = 0, 0
		v.float4Raw, other.float4Raw = 0, 0
		v.float4RawValid, other.float4RawValid = false, false
	}
	if v.hstore != nil || other.hstore != nil {
		if !v.validHstore() || !other.validHstore() || len(v.hstore.pairs) != len(other.hstore.pairs) {
			return false, nil
		}
		for i, pair := range v.hstore.pairs {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return false, err
				}
			}
			if pair != other.hstore.pairs[i] {
				return false, nil
			}
		}
		v.hstore, other.hstore = nil, nil
	}
	if v.lquery != nil || other.lquery != nil {
		if !v.validLquery() || !other.validLquery() || v.lquery.text != other.lquery.text {
			return false, nil
		}
		v.lquery, other.lquery = nil, nil
	}
	v.typ, other.typ = nil, nil
	if math.Float64bits(v.floating) != math.Float64bits(other.floating) {
		return false, nil
	}
	v.floating, other.floating = 0, 0
	if !v.sameGeometry(other) {
		return false, nil
	}
	v.geometry, other.geometry = nil, nil
	if v.path != nil || other.path != nil {
		if !samePath(v, other) {
			return false, nil
		}
		v.path, other.path = nil, nil
	}
	if v.ftsVector != nil || other.ftsVector != nil || v.ftsQuery != nil || other.ftsQuery != nil {
		same, err := v.sameFTS(ctx, other)
		if err != nil || !same {
			return same, err
		}
		v.ftsVector, other.ftsVector, v.ftsQuery, other.ftsQuery = nil, nil, nil, nil
	}
	if v == other {
		return true, nil
	}
	leftArray, rightArray := v.array, other.array
	leftRecord, rightRecord := v.composite, other.composite
	v.array, other.array, v.composite, other.composite = nil, nil, nil, nil
	if v != other {
		return false, nil
	}
	if leftArray != rightArray {
		if leftArray == nil || rightArray == nil || !slices.Equal(leftArray.dimensions, rightArray.dimensions) || len(leftArray.elements) != len(rightArray.elements) {
			return false, nil
		}
		for i, value := range leftArray.elements {
			same, err := value.SamePayload(ctx, rightArray.elements[i])
			if err != nil || !same {
				return same, err
			}
		}
	}
	if leftRecord != rightRecord {
		if leftRecord == nil || rightRecord == nil || len(leftRecord.fields) != len(rightRecord.fields) {
			return false, nil
		}
		for i, value := range leftRecord.fields {
			same, err := value.SamePayload(ctx, rightRecord.fields[i])
			if err != nil || !same {
				return same, err
			}
		}
	}
	return true, ctx.Err()
}
