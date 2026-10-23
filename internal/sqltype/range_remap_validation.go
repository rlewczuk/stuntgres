package sqltype

import (
	"context"
	"fmt"
)

// RequiresRangeRemapValidation identifies alias-bearing custom range containers.
// Ordinary scalar generations and builtin ranges retain the no-provider fast path.
func (d *Descriptor) RequiresRangeRemapValidation() bool {
	if d == nil {
		return false
	}
	d = d.BaseType()
	if r := d.RangeType(); r != nil {
		if r.customRange == nil {
			return false
		}
		s := r.rangeSubtype
		return s.ContainsRegclass() || s.ContainsRegtype() || s.ContainsRegObject() || s.ContainsRegproc() || s.ContainsOperatorAlias() || s.ContainsRegTS() || s.ContainsRegrole()
	}
	if d.IsArray() {
		return d.Element().RequiresRangeRemapValidation()
	}
	if d.IsComposite() {
		for i := range d.CompositeLen() {
			if d.CompositeMember(i).Usage.Type.RequiresRangeRemapValidation() {
				return true
			}
		}
	}
	return false
}

// ValidateMappedRanges runs only after identity mapping in an isolated candidate,
// never under the destination catalog mutex. Structural endpoint constraints
// preserve actual order/equality; verify that invariant with the destination
// subtype provider, including multirange ordering and normalization. No endpoint
// swapping or silent conversion to empty is permitted.
func (v Value) ValidateMappedRanges(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.IsNull() || !v.Type().RequiresRangeRemapValidation() {
		return nil
	}
	d := v.Type().BaseType()
	if r := d.RangeType(); r != nil {
		validate := func(member rangeDatum) error {
			if !r.validRangeDatum(member) {
				return fmt.Errorf("invalid mapped range endpoint generation")
			}
			canonical, err := r.canonicalRange(ctx, member, false)
			if err != nil {
				if ctx.Err() != nil {
					return err
				}
				if member.flags&(rangeLowerUnbounded|rangeUpperUnbounded) == 0 {
					return fmt.Errorf("stuntgres: imported alias range ordering is unsatisfiable for endpoint identities %s and %s: %w", member.lower.value.text, member.upper.value.text, err)
				}
				return err
			}
			if canonical != member {
				return fmt.Errorf("identity mapping changed range canonical form")
			}
			return nil
		}
		if d.IsRange() {
			return validate(v.rangeDatum)
		}
		if v.multirange == nil {
			return fmt.Errorf("invalid mapped multirange payload")
		}
		for _, member := range v.multirange.members {
			if err := validate(member); err != nil {
				return err
			}
		}
		return v.validateMultirange(ctx)
	}
	if d.IsArray() {
		for i := range v.ArrayLen() {
			if err := v.ArrayElement(i).ValidateMappedRanges(ctx); err != nil {
				return err
			}
		}
	}
	if d.IsComposite() {
		for i := range d.CompositeLen() {
			if err := v.CompositeField(i).ValidateMappedRanges(ctx); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
