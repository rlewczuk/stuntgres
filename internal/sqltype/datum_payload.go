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
	if v.ddlCommand != nil || other.ddlCommand != nil {
		return v.typ != nil && other.typ != nil && v.typ.ID() == 32 && other.typ.ID() == 32 &&
			v.ValidResult(v.typ.Usage()) && other.ValidResult(other.typ.Usage()) &&
			v.ddlCommand != nil && other.ddlCommand != nil &&
			v.ddlCommand.command == other.ddlCommand.command, nil
	}
	if v.xml != nil || other.xml != nil || v.typ != nil && v.typ.BaseType().id == XML || other.typ != nil && other.typ.BaseType().id == XML {
		if !v.validXML() || !other.validXML() {
			return false, nil
		}
		if v.xml != other.xml {
			a, b := v.xml.result, other.xml.result
			if a.RequestedMode() != b.RequestedMode() || a.EffectiveMode() != b.EffectiveMode() || len(a.Original()) != len(b.Original()) {
				return false, nil
			}
			// Declaration metadata is derived from the accepted spelling and mode.
			// Compare that spelling without copying or an unbounded whole-string scan.
			left, right := a.Original(), b.Original()
			for start := 0; start < len(left); start += 4096 {
				if err := ctx.Err(); err != nil {
					return false, err
				}
				end := min(start+4096, len(left))
				if left[start:end] != right[start:end] {
					return false, nil
				}
			}
		}
		v.xml, other.xml = nil, nil
	}
	if v.statisticArray != nil || other.statisticArray != nil {
		if !v.validStatisticResult() || !other.validStatisticResult() || v.statisticArray == nil || other.statisticArray == nil {
			return false, nil
		}
		a, b := v.statisticArray, other.statisticArray
		if a.element.Type.ID() != b.element.Type.ID() || a.element.Typmod != b.element.Typmod || a.element.Collation != b.element.Collation {
			return false, nil
		}
		same, err := a.value.SamePayload(ctx, b.value)
		if err != nil || !same {
			return same, err
		}
		v.statisticArray, other.statisticArray = nil, nil
	}
	if v.typ != nil && other.typ != nil && (v.typ.BaseType().IsSeg() || other.typ.BaseType().IsSeg()) {
		if !v.typ.BaseType().IsSeg() || !other.typ.BaseType().IsSeg() || !v.seg.valid() || !other.seg.valid() || v.seg != other.seg {
			return false, nil
		}
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
	if v.ltxtquery != nil || other.ltxtquery != nil {
		if !v.validLtxtquery() || !other.validLtxtquery() || v.ltxtquery.text != other.ltxtquery.text {
			return false, nil
		}
		v.ltxtquery, other.ltxtquery = nil, nil
	}
	if v.ltree != nil || other.ltree != nil {
		if !v.validLtree() || !other.validLtree() || v.ltree.text != other.ltree.text {
			return false, nil
		}
		v.ltree, other.ltree = nil, nil
	}
	if v.statistics != nil || other.statistics != nil {
		if !v.validStatistics() || !other.validStatistics() || *v.statistics != *other.statistics {
			return false, nil
		}
		v.statistics, other.statistics = nil, nil
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
