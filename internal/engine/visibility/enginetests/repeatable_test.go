package enginetests

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

func TestRepeatableConflictRepairedZeroBirth(t *testing.T) {
	// A retained rolled-back insertion still has Born 0 after repair. Its
	// canonical repaired header, not a fabricated committed birth, carries
	// a genuine later DELETE's committed end.
	aborted := storage.RowVersion{Row: 1, Version: 10, Born: 0, InsertFullXID: 31}
	repaired, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{aborted})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		stable bool
		end    ids.CommitSeq
		want   string
	}{
		{"later_committed_delete", true, 6, "could not serialize access due to concurrent delete"},
		{"no_committed_end", true, 0, ""},
		{"end_at_frontier", true, 5, ""},
		{"end_before_frontier", true, 4, ""},
		{"fresh_snapshot_later_delete", false, 6, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := repaired
			if tc.end != 0 {
				headers = storage.CommitHeapEnds(repaired, []storage.RowVersion{repaired.Apply(aborted)}, 40, tc.end)
			}
			head := headers.Apply(aborted)
			if head.Born != 0 || !head.ForcedFrozen || head.ForcedEnd != tc.end {
				t.Fatal("repair/end projection lost zero-birth committed-end state", head)
			}
			assertRepeatableDiagnostic(t, visibility.RepeatableConflict(tc.stable, 5, head, nil), tc.want)
			if before := repaired.Apply(aborted); before.ForcedEnd != 0 || before.DeleteFullXID != 0 || before.Born != 0 || !before.ForcedFrozen {
				t.Fatal("later end changed captured repaired root", before)
			}
		})
	}
}

func TestRepeatableConflictOwnWriteAndPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stable        bool
		born          ids.CommitSeq
		repaired      bool
		physicalEnd   ids.CommitSeq
		markerEnd     ids.CommitSeq
		markerVersion ids.VersionID
		want          string
	}{
		{name: "ordinary_own_write", stable: true},
		{name: "ordinary_own_write_physical_end", stable: true, physicalEnd: 6},
		{name: "ordinary_own_write_end_marker", stable: true, markerEnd: 6, markerVersion: 10},
		{name: "ordinary_unchanged", stable: true, born: 2},
		{name: "birth_at_frontier", stable: true, born: 5},
		{name: "concurrent_update", stable: true, born: 6, want: "could not serialize access due to concurrent update"},
		{name: "update_precedes_physical_delete", stable: true, born: 6, physicalEnd: 7, want: "could not serialize access due to concurrent update"},
		{name: "update_precedes_end_marker", stable: true, born: 6, markerEnd: 7, markerVersion: 10, want: "could not serialize access due to concurrent update"},
		{name: "ordinary_physical_delete", stable: true, born: 2, physicalEnd: 6, want: "could not serialize access due to concurrent delete"},
		{name: "ordinary_physical_end_at_frontier", stable: true, born: 2, physicalEnd: 5},
		{name: "ordinary_physical_end_before_frontier", stable: true, born: 2, physicalEnd: 4},
		{name: "ordinary_exact_end_marker", stable: true, born: 2, markerEnd: 6, markerVersion: 10, want: "could not serialize access due to concurrent delete"},
		{name: "ordinary_end_marker_at_frontier", stable: true, born: 2, markerEnd: 5, markerVersion: 10},
		{name: "ordinary_end_marker_before_frontier", stable: true, born: 2, markerEnd: 4, markerVersion: 10},
		{name: "sibling_end_is_not_target_end", stable: true, born: 2, markerEnd: 6, markerVersion: 11},
		{name: "fresh_snapshot_update_and_delete", born: 6, physicalEnd: 7, markerEnd: 7, markerVersion: 10},
		{name: "repair_clears_ordinary_end_authority", stable: true, repaired: true, markerEnd: 6, markerVersion: 10},
		{name: "repair_clears_old_birth_authority", stable: true, born: 6, repaired: true},
		{name: "repair_end_precedes_old_birth", stable: true, born: 6, repaired: true, physicalEnd: 7, want: "could not serialize access due to concurrent delete"},
		{name: "repaired_committed_delete", stable: true, born: 2, repaired: true, physicalEnd: 6, want: "could not serialize access due to concurrent delete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			head := storage.RowVersion{Row: 1, Version: 10, Born: tc.born, ForcedFrozen: tc.repaired, ForcedEnd: tc.physicalEnd}
			var ends *storage.DeletionRoot
			if tc.markerEnd != 0 {
				ends = &storage.DeletionRoot{}
				ends.Set(head.Row, storage.RowEnd{Version: tc.markerVersion, End: tc.markerEnd})
			}
			assertRepeatableDiagnostic(t, visibility.RepeatableConflict(tc.stable, 5, head, ends), tc.want)
		})
	}
}

func assertRepeatableDiagnostic(t *testing.T, err error, message string) {
	t.Helper()
	if message == "" {
		if err != nil {
			t.Fatal("unexpected stable-snapshot conflict", err)
		}
		return
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		t.Fatalf("got %v, want complete 40001 diagnostic: %s", err, message)
	}
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: message}
	if *diagnostic != want {
		t.Fatalf("diagnostic %+v, want %+v", *diagnostic, want)
	}
}
