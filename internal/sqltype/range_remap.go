package sqltype

import "fmt"

// RangeMapMode distinguishes descriptor reownership from comparison-changing
// identity import. Identity imports require ValidateMappedRanges outside catalog
// locks before publication; mapping itself never invokes a subtype provider.
type RangeMapMode uint8

const (
	RangeMapGeneration RangeMapMode = iota
	RangeMapIdentity
)

func (v Value) MapRange(target *Descriptor, mode RangeMapMode, leaf func(Value, *Descriptor) (Value, error)) (Value, error) {
	if v.typ == nil || target == nil || leaf == nil || mode > RangeMapIdentity {
		return Value{}, fmt.Errorf("missing custom range mapping authority")
	}
	from, to := v.typ.BaseType(), target.BaseType()
	if !from.IsRange() && !from.IsMultirange() || from.IsRange() != to.IsRange() || from.IsMultirange() != to.IsMultirange() || from.RangeType().customRange == nil || to.RangeType().customRange == nil {
		return Value{}, fmt.Errorf("incompatible custom range mapping")
	}
	if v.null {
		return Null(target), nil
	}
	if from.IsRange() && !v.validRange() || from.IsMultirange() && !v.validMultirangeShape() {
		return Value{}, fmt.Errorf("invalid custom range mapping source")
	}
	force := target != v.typ
	mapDatum := func(datum rangeDatum) (rangeDatum, error) {
		if datum.flags&rangeEmpty != 0 || datum.flags&(rangeLowerUnbounded|rangeUpperUnbounded) == rangeLowerUnbounded|rangeUpperUnbounded {
			return datum, nil
		}
		var lower, upper Value
		changed := force
		if datum.flags&rangeLowerUnbounded == 0 {
			mapped, err := leaf(*datum.lower.value, to.RangeType().rangeSubtype)
			if err != nil {
				return rangeDatum{}, err
			}
			if mapped.typ != to.RangeType().rangeSubtype || mapped.null {
				return rangeDatum{}, fmt.Errorf("foreign imported range endpoint")
			}
			if mode == RangeMapGeneration {
				original := *datum.lower.value
				original.typ = mapped.typ
				if !SameDatum(original, mapped) {
					return rangeDatum{}, fmt.Errorf("comparison-changing generation-only range mapping")
				}
			}
			lower = mapped
			changed = changed || !SameDatum(mapped, *datum.lower.value)
		}
		if datum.flags&rangeUpperUnbounded == 0 {
			mapped, err := leaf(*datum.upper.value, to.RangeType().rangeSubtype)
			if err != nil {
				return rangeDatum{}, err
			}
			if mapped.typ != to.RangeType().rangeSubtype || mapped.null {
				return rangeDatum{}, fmt.Errorf("foreign imported range endpoint")
			}
			if mode == RangeMapGeneration {
				original := *datum.upper.value
				original.typ = mapped.typ
				if !SameDatum(original, mapped) {
					return rangeDatum{}, fmt.Errorf("comparison-changing generation-only range mapping")
				}
			}
			upper = mapped
			changed = changed || !SameDatum(mapped, *datum.upper.value)
		}
		if !changed {
			return datum, nil
		}
		pair := &rangeEndpointPair{}
		if datum.flags&rangeLowerUnbounded == 0 {
			pair.lower = lower
			datum.lower = rangeBound{value: &pair.lower}
		}
		if datum.flags&rangeUpperUnbounded == 0 {
			pair.upper = upper
			datum.upper = rangeBound{value: &pair.upper}
		}
		return datum, nil
	}
	if from.IsRange() {
		datum, err := mapDatum(v.rangeDatum)
		return Value{typ: target, rangeDatum: datum}, err
	}
	var members []rangeDatum
	if force {
		members = make([]rangeDatum, len(v.multirange.members))
	}
	for i, datum := range v.multirange.members {
		mapped, err := mapDatum(datum)
		if err != nil {
			return Value{}, err
		}
		if members == nil && mapped != datum {
			members = append([]rangeDatum(nil), v.multirange.members...)
		}
		if members != nil {
			members[i] = mapped
		}
	}
	if members == nil && !force {
		return v, nil
	}
	return Value{typ: target, multirange: &multirangePayload{members: members}}, nil
}
