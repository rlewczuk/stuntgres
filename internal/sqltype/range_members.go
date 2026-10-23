package sqltype

import "fmt"

// RangeBounds exposes detached immutable endpoint values for native structural
// consumers. An omitted endpoint is a typed NULL; SQL NULL is not a range datum.
type RangeBounds struct {
	Lower, Upper                   Value
	LowerInclusive, UpperInclusive bool
	LowerUnbounded, UpperUnbounded bool
	Empty                          bool
}

func (v Value) RangeBounds() (RangeBounds, error) {
	if !v.validRange() {
		return RangeBounds{}, fmt.Errorf("invalid range traversal datum")
	}
	d := v.typ.BaseType()
	x := v.rangeDatum
	bounds := RangeBounds{Lower: Null(d.rangeSubtype), Upper: Null(d.rangeSubtype), LowerInclusive: x.flags&rangeLowerInclusive != 0, UpperInclusive: x.flags&rangeUpperInclusive != 0, LowerUnbounded: x.flags&rangeLowerUnbounded != 0, UpperUnbounded: x.flags&rangeUpperUnbounded != 0, Empty: x.flags&rangeEmpty != 0}
	if bounds.Empty {
		return bounds, nil
	}
	value := func(b rangeBound) Value {
		if d.customRange != nil {
			return *b.value
		}
		if d.rangeSubtype.id == Numeric {
			return b.numericValue(d.rangeSubtype)
		}
		return Value{typ: d.rangeSubtype, integer: b.magnitude}
	}
	if !bounds.LowerUnbounded {
		bounds.Lower = value(x.lower)
	}
	if !bounds.UpperUnbounded {
		bounds.Upper = value(x.upper)
	}
	return bounds, nil
}

func (v Value) MultirangeLen() int {
	if v.null || v.multirange == nil {
		return 0
	}
	return len(v.multirange.members)
}
func (v Value) MultirangeMember(i int) Value {
	return Value{typ: v.typ.BaseType().multirangeRange, rangeDatum: v.multirange.members[i]}
}

// VisitRangeEndpoints visits only present immutable leaves. Domains preserve the
// same physical container; empty, unbounded and NULL payloads have no leaves.
func (v Value) VisitRangeEndpoints(visit func(Value) error) error {
	if v.IsNull() {
		return nil
	}
	if v.Type() == nil || visit == nil {
		return fmt.Errorf("missing range traversal authority")
	}
	if v.Type().BaseType().IsMultirange() {
		for i := range v.MultirangeLen() {
			if err := v.MultirangeMember(i).VisitRangeEndpoints(visit); err != nil {
				return err
			}
		}
		return nil
	}
	bounds, err := v.RangeBounds()
	if err != nil {
		return err
	}
	if !bounds.Empty {
		if !bounds.LowerUnbounded {
			if err := visit(bounds.Lower); err != nil {
				return err
			}
		}
		if !bounds.UpperUnbounded {
			if err := visit(bounds.Upper); err != nil {
				return err
			}
		}
	}
	return nil
}
