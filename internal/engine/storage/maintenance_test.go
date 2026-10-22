package storage

import "testing"

func TestVacuumF2BirthHorizon(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		bound, publisher, want uint64
	}{
		{"unknown remains uninitialized", 0, 7, 0},
		{"older actual publisher", 9, 7, 7},
		{"newer birth cannot advance", 7, 9, 7},
		{"same publisher", 7, 7, 7},
		{"full width across wrap", 0x100000003, 0xffffffff, 0xffffffff},
		{"full width newer epoch", 0xffffffff, 0x100000003, 0xffffffff},
		{"unknown provenance invalidates bound", 7, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Maintenance{FrozenXID: tc.bound, Live: 2, Dead: 1, Scanned: true, Tuples: 2, Inserts: 5, Updates: 3, Deletes: 1, Modifications: 9, InsertsSince: 1, VacuumCount: 4}
			want := m
			want.FrozenXID = tc.want
			m.RecordBirth(tc.publisher)
			if m != want {
				t.Fatalf("birth changed unjustified bound or unrelated facts: got %#v want %#v", m, want)
			}
		})
	}
}
