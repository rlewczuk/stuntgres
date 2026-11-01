package core

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func TestTriggerTransitionImageDemand(t *testing.T) {
	for _, oldOnly := range []bool{true, false} {
		old, next := make([]sqltype.Value, 2), make([]sqltype.Value, 2)
		images := afterRowImages{}
		images.detach(old, next, oldOnly, !oldOnly)
		if oldOnly {
			if images.next != nil || images.old == nil || &images.old[0] == &old[0] {
				t.Fatal("OLD-only capture copied unused NEW or borrowed OLD")
			}
		} else if images.old != nil || images.next == nil || &images.next[0] == &next[0] {
			t.Fatal("NEW-only capture copied unused OLD or borrowed NEW")
		}
		ownedOld, ownedNext := images.old, images.next
		images.detach(old, next, true, true) // A qualifying scalar ROW consumer.
		if images.old == nil || images.next == nil || &images.old[0] == &old[0] || &images.next[0] == &next[0] {
			t.Fatal("scalar consumer did not own both images")
		}
		if oldOnly && &ownedOld[0] != &images.old[0] || !oldOnly && &ownedNext[0] != &images.next[0] {
			t.Fatal("scalar consumer recopied an already owned transition image")
		}
		var retained afterRowImages
		allocations := testing.AllocsPerRun(10, func() {
			retained = afterRowImages{}
			retained.detach(old, next, oldOnly, !oldOnly)
		})
		if allocations != 1 {
			t.Fatal("one-sided transition made unused image allocations", allocations)
		}
		allocations = testing.AllocsPerRun(10, func() {
			retained = afterRowImages{}
			retained.detach(old, next, oldOnly, !oldOnly)
			retained.detach(old, next, true, true)
		})
		if allocations != 2 || retained.old == nil || retained.next == nil {
			t.Fatal("scalar consumer recopied accepted images", allocations)
		}
	}
}
