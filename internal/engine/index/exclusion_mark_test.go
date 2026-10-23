package index

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestExclusionMarkNilWhenZero(t *testing.T) {
	if s := ExclusionMarkSlotOf(ExclusionModes{}, nil); s != nil {
		t.Fatalf("zero exclusion state stored slot %v", s)
	}
	if got := ExclusionMarkOrZero(nil); !got.Modes.Zero() || got.Projection != nil {
		t.Fatalf("nil slot reads as %+v, want zero", got)
	}
}

func TestExclusionMarkRestoresCapturedState(t *testing.T) {
	byConstraint := map[ids.OID]bool{7: true}
	projection := &ExclusionProjection{}
	cases := map[string]struct {
		modes      ExclusionModes
		projection *ExclusionProjection
	}{
		"flags":            {modes: ExclusionModes{AllSet: true, AllDeferred: true}},
		"constraint modes": {modes: ExclusionModes{ByConstraint: byConstraint}},
		"empty events":     {modes: ExclusionModes{Events: []ExclusionEvent{}}},
		"projection only":  {projection: projection},
	}
	for name, c := range cases {
		s := ExclusionMarkSlotOf(c.modes, c.projection)
		if s == nil {
			t.Fatalf("%s: nonzero exclusion state stored a nil slot", name)
		}
		got := ExclusionMarkOrZero(s)
		if !reflect.DeepEqual(got.Modes, c.modes) || got.Projection != c.projection {
			t.Fatalf("%s: restored %+v, want modes %+v projection %p", name, got, c.modes, c.projection)
		}
		if (got.Modes.Events == nil) != (c.modes.Events == nil) {
			t.Fatalf("%s: restored events nil-ness differs", name)
		}
	}
	if got := ExclusionMarkOrZero(ExclusionMarkSlotOf(ExclusionModes{ByConstraint: byConstraint}, nil)); reflect.ValueOf(got.Modes.ByConstraint).Pointer() != reflect.ValueOf(byConstraint).Pointer() {
		t.Fatal("restored constraint modes are not the captured map")
	}
}
