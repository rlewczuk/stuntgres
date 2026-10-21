package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

func TestExclusionMarkNilWhenZero(t *testing.T) {
	if s := exclusionMarkSlotOf(index.ExclusionModes{}, nil); s != nil {
		t.Fatalf("zero exclusion state stored slot %v", s)
	}
	if got := exclusionMarkOrZero(nil); !got.modes.Zero() || got.projection != nil {
		t.Fatalf("nil slot reads as %+v, want zero", got)
	}
}

func TestExclusionMarkRestoresCapturedState(t *testing.T) {
	byConstraint := map[ids.OID]bool{7: true}
	projection := &index.ExclusionProjection{}
	cases := map[string]struct {
		modes      index.ExclusionModes
		projection *index.ExclusionProjection
	}{
		"flags":            {modes: index.ExclusionModes{AllSet: true, AllDeferred: true}},
		"constraint modes": {modes: index.ExclusionModes{ByConstraint: byConstraint}},
		"empty events":     {modes: index.ExclusionModes{Events: []index.ExclusionEvent{}}},
		"projection only":  {projection: projection},
	}
	for name, c := range cases {
		s := exclusionMarkSlotOf(c.modes, c.projection)
		if s == nil {
			t.Fatalf("%s: nonzero exclusion state stored a nil slot", name)
		}
		got := exclusionMarkOrZero(s)
		if !reflect.DeepEqual(got.modes, c.modes) || got.projection != c.projection {
			t.Fatalf("%s: restored %+v, want modes %+v projection %p", name, got, c.modes, c.projection)
		}
		if (got.modes.Events == nil) != (c.modes.Events == nil) {
			t.Fatalf("%s: restored events nil-ness differs", name)
		}
	}
	if got := exclusionMarkOrZero(exclusionMarkSlotOf(index.ExclusionModes{ByConstraint: byConstraint}, nil)); reflect.ValueOf(got.modes.ByConstraint).Pointer() != reflect.ValueOf(byConstraint).Pointer() {
		t.Fatal("restored constraint modes are not the captured map")
	}
}
