package session

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func TestSerializableWriteSurvives(t *testing.T) {
	rewritten, deleted, written, other := ids.ObjectAddress{ClassID: 1259, ObjectID: 1}, ids.ObjectAddress{ClassID: 1259, ObjectID: 2}, ids.ObjectAddress{ClassID: 1259, ObjectID: 3}, ids.ObjectAddress{ClassID: 1259, ObjectID: 4}
	owner := &TransactionOwner{
		Catalog: &Journal{Storage: map[ids.ObjectAddress]StorageChange{rewritten: {Rewritten: true}, other: {}}},
		Deletes: map[ids.ObjectAddress]*visibility.PrivateDeletes{deleted: {Rows: map[ids.RowID]visibility.DeleteCandidate{7: {Version: 70}}}},
		Writes:  map[ids.ObjectAddress][]storage.RowVersion{written: {{Row: 8, Version: 80}, {Row: 9, Version: 90}}},
	}
	for _, tc := range []struct {
		name    string
		address ids.ObjectAddress
		event   visibility.SerializableWrite
		want    bool
	}{
		{"rewrite kept", rewritten, visibility.SerializableWrite{Rewrite: true}, true},
		{"rewrite undone", other, visibility.SerializableWrite{Rewrite: true}, false},
		{"deletion kept", deleted, visibility.SerializableWrite{Deletion: true, Row: 7, Base: 70}, true},
		{"deletion of another base", deleted, visibility.SerializableWrite{Deletion: true, Row: 7, Base: 71}, false},
		{"deletion of another row", deleted, visibility.SerializableWrite{Deletion: true, Row: 6, Base: 70}, false},
		{"deletion without private deletes", other, visibility.SerializableWrite{Deletion: true, Row: 7, Base: 70}, false},
		{"write kept", written, visibility.SerializableWrite{Version: 90}, true},
		{"write undone", written, visibility.SerializableWrite{Version: 91}, false},
	} {
		if got := owner.SerializableWriteSurvives(tc.address, tc.event); got != tc.want {
			t.Errorf("%s: SerializableWriteSurvives = %v, want %v", tc.name, got, tc.want)
		}
	}
	if (&TransactionOwner{}).SerializableWriteSurvives(rewritten, visibility.SerializableWrite{Rewrite: true}) {
		t.Error("a rewrite survived without a journal")
	}
}
