package index

import "stuntgres/internal/engine/session"

// ExclusionMark is the exclusion constraint state a savepoint captured: the
// owner's exclusion modes and the engine's pending exclusion projection. It
// implements session.ExclusionMarkSlot.
type ExclusionMark struct {
	Modes      ConstraintModes
	Projection *ExclusionProjection
}

func (*ExclusionMark) ExclusionMarkSlot() {}

// ExclusionMarkSlotOf stores modes and projection. Exactly zero state (see
// ConstraintModes.Zero, nil projection) stays a nil slot, so restoring it
// reproduces the captured value.
func ExclusionMarkSlotOf(modes ConstraintModes, projection *ExclusionProjection) session.ExclusionMarkSlot {
	if projection == nil && modes.Zero() {
		return nil
	}
	return &ExclusionMark{Modes: modes, Projection: projection}
}

// ExclusionMarkOrZero returns the captured exclusion state; a nil slot is zero.
func ExclusionMarkOrZero(s session.ExclusionMarkSlot) ExclusionMark {
	if m, _ := s.(*ExclusionMark); m != nil {
		return *m
	}
	return ExclusionMark{}
}
