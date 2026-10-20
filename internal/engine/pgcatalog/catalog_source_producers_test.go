package pgcatalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestProducerRegistryLookup(t *testing.T) {
	entries := Registrations()
	if len(entries) == 0 {
		t.Fatal("empty producer registry")
	}
	for i, entry := range entries {
		if entry.Produce == nil {
			t.Fatal("registered nil producer", entry.OID)
		}
		if i > 0 && entries[i-1].OID >= entry.OID {
			t.Fatal("registry not strictly ordered by OID", entries[i-1].OID, entry.OID)
		}
		if _, ok := ProducerFor(entry.OID); !ok {
			t.Fatal("registered OID not found", entry.OID)
		}
		if i > 0 && entries[i-1].OID+1 < entry.OID {
			if _, ok := ProducerFor(entry.OID - 1); ok {
				t.Fatal("gap OID resolved", entry.OID-1)
			}
		}
	}
	// The engine dispatches these explicitly; the registry must not claim them.
	for _, oid := range []ids.OID{0, LocksOID, 12048, entries[len(entries)-1].OID + 1} {
		if _, ok := ProducerFor(oid); ok {
			t.Fatal("unregistered OID resolved", oid)
		}
	}
	entries[0].Produce, entries[0].OID = nil, 0
	if again := Registrations(); again[0].Produce == nil || again[0].OID == 0 {
		t.Fatal("Registrations exposed the shared registry")
	}
}
