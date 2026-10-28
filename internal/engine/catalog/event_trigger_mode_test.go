package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
	"testing"
)

func TestEventTriggerMode(t *testing.T) {
	empty := NewRead(nil, NewState(1))
	// Keep the caller's iterator callback outside the measured lookup. A range
	// callback passed through an iterator may escape independently of the index.
	reject := func(ids.ObjectAddress, *EventTrigger) bool { panic("nonempty event index") }
	for _, tc := range []struct {
		name string
		call func()
	}{
		{"lookup", func() { empty.EventTriggers()(reject) }},
		{"capture", func() {
			if CollectDroppedObjects(empty) {
				panic("absent capture")
			}
		}},
	} {
		allocations, bytes := util.AllocationsPerCall(100, tc.call)
		if allocations != 0 || bytes != 0 {
			t.Fatal("absent event index", tc.name, "allocated", allocations, bytes)
		}
	}
	for _, tc := range []struct {
		mode            TriggerMode
		origin, replica bool
	}{{TriggerOrigin, true, false}, {TriggerDisabled, false, false}, {TriggerReplica, false, true}, {TriggerAlways, true, true}, {0, false, false}, {'X', false, false}} {
		d := &EventTrigger{Event: "sql_drop", FiringMode: tc.mode}
		valid := tc.mode != 0 && tc.mode != 'X'
		if tc.mode.Valid() != valid {
			t.Fatal("mode validity", tc.mode)
		}
		for _, replica := range []bool{false, true} {
			want := tc.origin
			if replica {
				want = tc.replica
			}
			if valid && tc.mode.Fires(replica) != want {
				t.Fatal("firing matrix", tc.mode, replica)
			}
			if d.Matches("sql_drop", "DROP TABLE", replica) != want || d.Matches("ddl_command_end", "DROP TABLE", replica) {
				t.Fatal("event matching", tc.mode, replica)
			}
			d.Tags = []string{"DROP VIEW", "DROP TABLE", "DROP TABLE"}
			if d.Matches("sql_drop", "DROP TABLE", replica) != want || d.Matches("sql_drop", "ALTER TABLE", replica) {
				t.Fatal("tag matching", tc.mode, replica)
			}
			d.Tags = []string{}
			if d.Matches("sql_drop", "DROP TABLE", replica) {
				t.Fatal("empty filter matched")
			}
			d.Tags = nil
		}
		predecessor := *d
		successor := *d
		successor.FiringMode = TriggerDisabled
		successor.ObjectName = "renamed"
		if predecessor.FiringMode != tc.mode || predecessor.ObjectName != "" {
			t.Fatal("mutated predecessor")
		}
	}
}
