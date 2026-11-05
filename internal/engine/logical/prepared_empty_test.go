package logical

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestLogicalHistoryRollbackOnlyPreparedRetention(t *testing.T) {
	for _, finish := range []RecordKind{CommitPrepared, RollbackPrepared} {
		name := "commit"
		if finish == RollbackPrepared {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			var h History
			var positions Allocator
			var textType *sqltype.Descriptor
			for _, typ := range sqltype.Builtins() {
				if typ.ID() == sqltype.Text {
					textType = typ
					break
				}
			}
			if textType == nil {
				t.Fatal("text descriptor missing")
			}
			value, err := textType.Input(context.Background(), "discarded", -1)
			if err != nil {
				t.Fatal(err)
			}
			first := positions.Allocate()
			anchor := h.Anchor(10, first)
			relation := &Relation{OID: 1, Schema: "public", Name: "t", Columns: []Column{{Name: "v", Number: 1, Usage: textType.Usage(), TypeName: "text"}}}
			change := &Change{LSN: first, Kind: Insert, TopXID: 10, SubXID: 11, Relation: relation, New: []sqltype.Value{value}, DecodedBytes: 80}
			h.Append(Record{Kind: RowEvent, LSN: first, Txn: anchor, Child: 11, Parent: 10, Change: change})
			h.Append(Record{Kind: SubAbort, LSN: positions.Allocate(), Txn: anchor, Child: 11, Parent: 10})
			activeSnapshot := h.Snapshot()
			activeRecords := slices.Clone(activeSnapshot)
			anchorValue := *anchor
			changeValue := *change
			values := slices.Clone(change.New)
			columns := slices.Clone(relation.Columns)
			valueBacking, columnBacking := &change.New[0], &relation.Columns[0]
			// PREPARE has no surviving row projection. Its real lifecycle
			// descriptor shares the original position, not mutable anchor data.
			prepared := &Transaction{XID: 10, FirstLSN: anchor.FirstLSN, GID: "rollback_only", PrepareLSN: positions.Allocate(), PrepareTime: 123}
			h.Append(Record{Kind: Prepare, LSN: prepared.PrepareLSN, Txn: prepared, Time: prepared.PrepareTime})
			preparedSnapshot := h.Snapshot()
			preparedRecords := slices.Clone(preparedSnapshot)
			preparedValue := *prepared
			confirmedPrepare := positions.End()
			if h.Active(10) != anchor || RestartLSN(preparedSnapshot, confirmedPrepare) != anchor.FirstLSN {
				t.Fatal("empty projection lost prepared anchor", h.Active(10), preparedSnapshot)
			}
			h.Trim(RestartLSN(preparedSnapshot, confirmedPrepare))
			if h.Len() != len(preparedSnapshot) {
				t.Fatal("prepared prefix released before terminal event")
			}
			h.Append(Record{Kind: finish, LSN: positions.Allocate(), Txn: prepared})
			finished := positions.End()
			if h.Active(10) != nil {
				t.Fatal("terminal event retained active anchor", h.Active(10))
			}
			if got := RestartLSN(h.Snapshot(), confirmedPrepare); got != anchor.FirstLSN {
				t.Fatal("lagging consumer lost rollback-only prefix", got)
			}
			if got := RestartLSN(h.Snapshot(), finished); got != finished {
				t.Fatal("consumed terminal event failed to release prefix", got, finished)
			}
			// Retain another open transaction while trimming the finished
			// prefix, proving new backing rather than a destructive compaction.
			appendEvent(&h, &positions, 20, 20, 20, 90, false)
			later := h.Active(20)
			h.Trim(RestartLSN(h.Snapshot(), positions.End()))
			if h.Len() != 1 || h.Snapshot()[0].Txn != later || &h.Snapshot()[0] == &preparedSnapshot[0] || h.Active(10) != nil || h.Active(20) != later {
				t.Fatal("trim retained finished prefix or lost next owner", h.Snapshot())
			}
			h.Append(Record{Kind: Commit, LSN: positions.Allocate(), Txn: later})
			h.Trim(RestartLSN(h.Snapshot(), positions.End()))
			if h.Len() != 0 || len(h.anchors) != 0 {
				t.Fatal("fully consumed history retained payload owners", h.Snapshot(), h.anchors)
			}
			h.Reset()
			if !reflect.DeepEqual(activeSnapshot, activeRecords) || !reflect.DeepEqual(preparedSnapshot, preparedRecords) || !reflect.DeepEqual(*anchor, anchorValue) || !reflect.DeepEqual(*prepared, preparedValue) || !reflect.DeepEqual(*change, changeValue) || !reflect.DeepEqual(change.New, values) || !reflect.DeepEqual(relation.Columns, columns) || &change.New[0] != valueBacking || &relation.Columns[0] != columnBacking {
				t.Fatal("finish/trim/reset mutated held snapshots or their descriptors")
			}
		})
	}
}
