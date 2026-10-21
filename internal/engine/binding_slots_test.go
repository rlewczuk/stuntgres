package engine

import (
	"stuntgres/internal/engine/catalog"
	"testing"
)

func TestBindingSlotsKeepNilAbsent(t *testing.T) {
	if ViewWriteSlotOf(nil) != nil || RuleCTEsSlotOf(nil) != nil {
		t.Fatal("a nil value must be stored as a nil slot")
	}
	if ViewWriteOf(nil) != nil || RuleCTEsOf(nil) != nil {
		t.Fatal("a nil slot must hold no value")
	}
	write, commands := &viewWrite{}, ruleCTECommands{}
	if ViewWriteOf(ViewWriteSlotOf(write)) != write {
		t.Fatal("a stored pointer must come back unchanged")
	}
	if got := RuleCTEsOf(RuleCTEsSlotOf(commands)); got == nil || len(got) != 0 {
		t.Fatal("an empty command map must stay present")
	}
}

func TestViewWriteSlotReportsBaseRelation(t *testing.T) {
	base := &catalog.Relation{}
	if got := ViewWriteSlotOf(&viewWrite{BaseRelation: base}).Base(); got != base {
		t.Fatal("the view-write slot must report the base relation the write lands on", got)
	}
}
