package engine

import "testing"

func TestBindingSlotsKeepNilAbsent(t *testing.T) {
	if insertConflictSlotOf(nil) != nil || viewWriteSlotOf(nil) != nil || ruleCTEsSlotOf(nil) != nil {
		t.Fatal("a nil value must be stored as a nil slot")
	}
	if insertConflictOf(nil) != nil || viewWriteOf(nil) != nil || ruleCTEsOf(nil) != nil {
		t.Fatal("a nil slot must hold no value")
	}
	conflict, write, commands := &boundInsertConflict{}, &viewWrite{}, ruleCTECommands{}
	if insertConflictOf(insertConflictSlotOf(conflict)) != conflict || viewWriteOf(viewWriteSlotOf(write)) != write {
		t.Fatal("a stored pointer must come back unchanged")
	}
	if got := ruleCTEsOf(ruleCTEsSlotOf(commands)); got == nil || len(got) != 0 {
		t.Fatal("an empty command map must stay present")
	}
}
