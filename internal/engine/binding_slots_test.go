package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/view"
	"testing"
)

func TestBindingSlotsKeepNilAbsent(t *testing.T) {
	if view.ViewWriteSlotOf(nil) != nil || view.RuleCTEsSlotOf(nil) != nil {
		t.Fatal("a nil value must be stored as a nil slot")
	}
	if view.ViewWriteOf(nil) != nil || view.RuleCTEsOf(nil) != nil {
		t.Fatal("a nil slot must hold no value")
	}
	write, commands := &view.Write{}, view.RuleCTECommands{}
	if view.ViewWriteOf(view.ViewWriteSlotOf(write)) != write {
		t.Fatal("a stored pointer must come back unchanged")
	}
	if got := view.RuleCTEsOf(view.RuleCTEsSlotOf(commands)); got == nil || len(got) != 0 {
		t.Fatal("an empty command map must stay present")
	}
}

func TestViewWriteSlotReportsBaseRelation(t *testing.T) {
	base := &catalog.Relation{}
	if got := view.ViewWriteSlotOf(&view.Write{BaseRelation: base}).Base(); got != base {
		t.Fatal("the view-write slot must report the base relation the write lands on", got)
	}
}
