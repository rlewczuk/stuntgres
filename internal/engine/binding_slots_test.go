package engine

import (
	"stuntgres/internal/engine/catalog"
	"testing"
)

func TestBindingSlotsKeepNilAbsent(t *testing.T) {
	if viewWriteSlotOf(nil) != nil || ruleCTEsSlotOf(nil) != nil {
		t.Fatal("a nil value must be stored as a nil slot")
	}
	if viewWriteOf(nil) != nil || ruleCTEsOf(nil) != nil {
		t.Fatal("a nil slot must hold no value")
	}
	write, commands := &viewWrite{}, ruleCTECommands{}
	if viewWriteOf(viewWriteSlotOf(write)) != write {
		t.Fatal("a stored pointer must come back unchanged")
	}
	if got := ruleCTEsOf(ruleCTEsSlotOf(commands)); got == nil || len(got) != 0 {
		t.Fatal("an empty command map must stay present")
	}
}

func TestViewWriteSlotReportsBaseRelation(t *testing.T) {
	base := &catalog.Relation{}
	if got := viewWriteSlotOf(&viewWrite{base: base}).Base(); got != base {
		t.Fatal("the view-write slot must report the base relation the write lands on", got)
	}
}
