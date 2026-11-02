package dml_test

import (
	"testing"

	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestLatestRowPhysicalHints(t *testing.T) {
	payload := make([]sqltype.Value, 1)
	original := storage.RowVersion{Row: 7, Version: 11, Born: 1, InsertFullXID: 31, Values: payload}
	sibling := storage.RowVersion{Row: 7, Version: 12, Born: 1, InsertFullXID: 32, Values: payload}
	successor := storage.RowVersion{Row: 7, Version: 20, Born: 2, InsertFullXID: 40, Values: payload, PrivateHistory: storage.RetainHeapPredecessor(original)}
	headers := storage.ReplaceHeapHeaders(nil, []storage.RowVersion{successor}, 40)
	headers = storage.CommitHeapEnds(headers, []storage.RowVersion{original}, 40, 2)
	successor.PrivateHistory = nil
	store := &storage.Relation{
		Versions: []storage.RowVersion{{Row: 8, Version: 8, Born: 1}, sibling, successor},
		Retained: &storage.HeapPredecessor{Version: original},
		Headers:  headers,
	}
	capture := storage.Relation{Versions: []storage.RowVersion{original, sibling}}
	readers := []struct {
		name string
		read func(dml.LatestRowSource, dml.RowRequest) (dml.LatestRow, error)
	}{
		{"update", dml.LatestUpdateRow},
		{"delete", dml.LatestDeleteRow},
		{"merge", dml.LatestMergeRow},
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			source := dml.LatestRowSource{Store: store, CommitSeq: 2}
			for _, tc := range []struct {
				name    string
				version ids.VersionID
				hint    int
				want    storage.RowVersion
				slot    int
			}{
				{"known", successor.Version, 2, successor, 2},
				{"stale_same_row", successor.Version, 1, successor, 2},
				{"successor_position", original.Version, 1, successor, 2},
				{"exact_sibling", sibling.Version, 2, sibling, 1},
			} {
				t.Run(tc.name, func(t *testing.T) {
					request := dml.RowRequest{Row: 7, Physical: tc.version, Hint: tc.hint, Storage: store}
					latest, err := reader.read(source, request)
					if err != nil || !latest.Live || latest.Version.Version != tc.want.Version || latest.Version.Row != tc.want.Row || latest.Hint != tc.slot || &latest.Version.Values[0] != &payload[0] {
						t.Fatal("latest lookup lost exact identity, current ordinal or payload backing", latest, err)
					}
				})
			}
			old, err := reader.read(dml.LatestRowSource{Store: &capture, CommitSeq: 1}, dml.RowRequest{Row: 7, Physical: original.Version, Hint: 0, Storage: &capture})
			if err != nil || !old.Live || old.Version.Version != original.Version || old.Hint != 0 || old.Version.HeapXmax() != 0 || capture.Headers != nil || capture.Versions[0].DeleteFullXID != 0 {
				t.Fatal("current lookup changed old capture authority", old, err)
			}
			additionalHeaders, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{original})
			if err != nil {
				t.Fatal(err)
			}
			additional := storage.Relation{Versions: []storage.RowVersion{sibling}, Headers: additionalHeaders}
			latest, err := reader.read(dml.LatestRowSource{Store: &additional, CommitSeq: 2}, dml.RowRequest{Row: 7, Physical: original.Version, Hint: 0, Storage: &additional})
			if err != nil || !latest.Live || latest.Version.Version != original.Version || latest.Version.HeapXmin() != 2 || latest.Hint != -1 {
				t.Fatal("additional physical version acquired a fabricated head ordinal", latest, err)
			}
			request := dml.RowRequest{Row: 7, Physical: successor.Version, Hint: 2, Storage: store}
			allocations := testing.AllocsPerRun(100, func() {
				latest, err := reader.read(source, request)
				if err != nil || !latest.Live || latest.Version.Version != successor.Version || latest.Hint != 2 {
					t.Fatal("known latest lookup failed", latest, err)
				}
			})
			if allocations != 0 {
				t.Fatal("known latest lookup allocated", allocations)
			}
		})
	}
}
