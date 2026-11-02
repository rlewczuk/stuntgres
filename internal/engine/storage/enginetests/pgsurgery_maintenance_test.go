package enginetests

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestPgsurgeryFreezeMaintenance(t *testing.T) {
	ctx := context.Background()
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
	}
	payload := []sqltype.Value{sqltype.Null(integer)}
	first := storage.RowVersion{Row: 1, Version: 1, Born: 1, InsertFullXID: 3, Values: payload}
	head := storage.RowVersion{Row: 1, Version: 2, Born: 2, InsertFullXID: 4, Values: payload}
	predecessor := storage.RetainHeapPredecessor(first)
	predecessor.End, predecessor.EndFullXID = 2, 4
	private := storage.RowVersion{Row: 2, Version: 3, InsertFullXID: 5, Values: payload}
	discarded, changed := storage.RetainDiscardedVersions(nil, []storage.RowVersion{private}, nil)
	if !changed {
		t.Fatal("real private version was not retained")
	}
	headers, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{first, private})
	if err != nil {
		t.Fatal(err)
	}
	store := storage.Relation{Versions: []storage.RowVersion{head}, Retained: predecessor, Discarded: discarded, Headers: headers, Generation: 7}
	effective := storage.EffectiveHeapRows(store.Versions, headers)
	if !storage.SameVersionRoot(effective, store.Versions) {
		t.Fatal("untargeted head vector copied")
	}
	candidate, err := storage.InspectVacuum(ctx, effective, nil, 2, 3, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err = storage.MaintainHeapHistory(ctx, candidate, store, 2, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Live != 3 || candidate.Removed != 0 || candidate.Headers != headers || candidate.Retained != predecessor || candidate.Discarded != discarded {
		t.Fatal("vacuum retired live repaired versions", candidate)
	}
	rewritten, err := storage.RewriteHeapVacuum(ctx, store, 2, 3, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rewritten.Versions) != 3 || rewritten.Live != 3 {
		t.Fatal("rewrite lost repaired siblings", rewritten)
	}
	for _, row := range rewritten.Versions {
		if &row.Values[0] != &payload[0] {
			t.Fatal("rewrite copied immutable payload")
		}
	}
	visible := storage.VisibleHeapVersions(&store, 2)
	if len(visible) != 3 || visible[0].Version != 2 || visible[1].Version != 1 || visible[2].Version != 3 {
		t.Fatal("visible capture lost exact siblings", visible)
	}
	if predecessor.Version.ForcedFrozen || private.ForcedFrozen || headers.Header(1).Slot != storage.HeapSlotNormal || len(store.Versions) != 1 {
		t.Fatal("old immutable capture mutated")
	}
	noOp, err := storage.MapHeapHeaderRows(headers, func(row storage.RowVersion) (storage.RowVersion, bool, error) { return row, false, nil })
	if err != nil || noOp != headers {
		t.Fatal("no-op header transport copied root", err)
	}
	mapped, err := storage.MapHeapHeaderRows(headers, func(row storage.RowVersion) (storage.RowVersion, bool, error) {
		row.Values = append([]sqltype.Value(nil), row.Values...)
		return row, true, nil
	})
	if err != nil || mapped == headers || &mapped.Additional[0].Values[0] == &payload[0] || &headers.Additional[0].Values[0] != &payload[0] {
		t.Fatal("payload transport ownership", err)
	}
	if mapped.Header(first.Version) != headers.Header(first.Version) || rewritten.Headers.Header(first.Version) != headers.Header(first.Version) ||
		headers.Header(first.Version).TupleLockVersion != first.Version || headers.Header(private.Version).TupleLockVersion != private.Version {
		t.Fatal("maintenance or payload transport lost exact repair anchors")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := storage.MaintainHeapHistory(canceled, candidate, store, 2, true, false, false); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled maintenance accepted", err)
	}
	if headers.Header(1).Slot != storage.HeapSlotNormal || predecessor.Next != nil {
		t.Fatal("canceled candidate mutated source")
	}
}

func TestPgsurgeryFreezeMaintenanceRawXmin(t *testing.T) {
	ctx := context.Background()
	payload := make([]sqltype.Value, 1)
	const epoch = uint64(1) << 32
	rows := []storage.RowVersion{
		{Row: 1, Version: 1, Born: 1, InsertFullXID: epoch + 31, DeleteFullXID: epoch + 40, Values: payload},
		{Row: 2, Version: 2, Born: 2, InsertFullXID: epoch + 32, Values: payload},
		{Row: 3, Version: 3, Born: 3, Frozen: true, ImportFrozen: true, Values: payload},
		{Row: 4, Version: 4, Born: 1, InsertFullXID: 2, Frozen: true, Values: payload},
	}
	wantXmin := [...]uint32{31, 32, 2, 2}
	for i, row := range rows {
		if row.HeapXmin() != wantXmin[i] {
			t.Fatalf("initial raw xmin for version %d = %d, want %d", row.Version, row.HeapXmin(), wantXmin[i])
		}
	}
	inspected, err := storage.InspectVacuum(ctx, rows, nil, 3, epoch+33, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Frozen != 2 || inspected.Live != 4 || inspected.Removed != 0 || inspected.Horizon != epoch+33 {
		t.Fatal("ordinary freeze selection or maintenance horizon changed", inspected)
	}
	candidate, err := storage.MaterializeVacuum(ctx, rows, nil, 3, epoch+33, inspected, nil)
	if err != nil {
		t.Fatal(err)
	}
	if storage.SameVersionRoot(rows, candidate.Versions) || len(candidate.Versions) != len(rows) || candidate.Headers != nil {
		t.Fatal("ordinary freeze did not detach only the changed version vector")
	}
	for i, row := range candidate.Versions {
		original := rows[i]
		if !row.Frozen || row.ForcedFrozen || row.RepairStamp != 0 || row.Row != original.Row || row.Version != original.Version ||
			row.Born != original.Born || row.InsertFullXID != original.InsertFullXID || row.DeleteFullXID != original.DeleteFullXID ||
			row.ImportFrozen != original.ImportFrozen || row.HeapXmin() != wantXmin[i] || &row.Values[0] != &payload[0] {
			t.Fatal("ordinary freeze changed raw header, identity or immutable payload", row)
		}
	}
	if rows[0].Frozen || rows[1].Frozen || rows[0].HeapXmin() != 31 || rows[0].HeapXmax() != 40 {
		t.Fatal("ordinary freeze mutated the old captured headers")
	}
	flags := &storage.HeapHeaderRoot{}
	flags.Versions = flags.Versions.Set(rows[1].Version, storage.HeapHeader{Frozen: true})
	flagged := flags.Apply(rows[1])
	if !flagged.Frozen || flagged.ForcedFrozen || flagged.HeapXmin() != 32 || rows[1].Frozen {
		t.Fatal("visibility-only header flag rewrote raw xmin or its old capture", flagged)
	}
	repaired, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{candidate.Versions[0]})
	if err != nil {
		t.Fatal(err)
	}
	effective := storage.EffectiveHeapRows(candidate.Versions, repaired)
	target := effective[0]
	if !target.ForcedFrozen || !target.Frozen || target.RepairStamp == 0 || target.HeapXmin() != 2 || target.HeapXmax() != 0 ||
		target.InsertFullXID != epoch+31 || target.Row != rows[0].Row || target.Version != rows[0].Version || &target.Values[0] != &payload[0] {
		t.Fatal("exact surgery failed to replace raw xmin/xmax while retaining insertion provenance", target)
	}
	for i := 1; i < len(effective); i++ {
		if effective[i].HeapXmin() != wantXmin[i] || effective[i].ForcedFrozen || effective[i].RepairStamp != 0 {
			t.Fatal("surgery changed an untouched frozen survivor or imported raw xmin", effective[i])
		}
	}
	if candidate.Versions[0].ForcedFrozen || candidate.Versions[0].HeapXmin() != 31 || candidate.Versions[0].HeapXmax() != 40 ||
		!candidate.Versions[2].ImportFrozen || candidate.Versions[2].InsertFullXID != 0 {
		t.Fatal("repair mutated its old ordinary/imported header captures")
	}
	again, err := storage.InspectVacuum(ctx, candidate.Versions, nil, 3, epoch+33, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err = storage.MaterializeVacuum(ctx, candidate.Versions, nil, 3, epoch+33, again, nil)
	if err != nil || again.Frozen != 0 || !storage.SameVersionRoot(again.Versions, candidate.Versions) {
		t.Fatal("unchanged ordinary freeze copied headers or lost maintenance state", again, err)
	}
}

func TestPgsurgeryFreezeMaintenanceSlots(t *testing.T) {
	ctx := context.Background()
	for _, state := range []storage.HeapSlotState{storage.HeapSlotRedirect, storage.HeapSlotDead, storage.HeapSlotUnused} {
		t.Run(map[storage.HeapSlotState]string{storage.HeapSlotRedirect: "redirect", storage.HeapSlotDead: "dead", storage.HeapSlotUnused: "unused"}[state], func(t *testing.T) {
			row := storage.RowVersion{Row: 1, Version: 1, Born: 1, InsertFullXID: 3}
			store := storage.Relation{Versions: []storage.RowVersion{row}}
			if state == storage.HeapSlotRedirect {
				store.Retained = storage.RetainHeapPredecessor(row)
				store.Retained.End, store.Retained.EndFullXID = 2, 4
				store.Versions = []storage.RowVersion{{Row: 1, Version: 2, Born: 2, InsertFullXID: 4}}
				successor := store.Versions[0]
				successor.PrivateHistory = storage.RetainHeapPredecessor(row)
				store.Headers = storage.ReplaceHeapHeaders(nil, []storage.RowVersion{successor}, 4)
			} else {
				store.Ends = &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 1, End: 2, EndFullXID: 4}}}
			}
			capturedHeaders := store.Headers
			c, err := storage.InspectVacuum(ctx, store.Versions, store.Ends, 2, 3, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			c, err = storage.MaterializeVacuum(ctx, store.Versions, store.Ends, 2, 3, c, nil)
			if err != nil {
				t.Fatal(err)
			}
			c, err = storage.MaintainHeapHistory(ctx, c, store, 2, true, state == storage.HeapSlotDead, state == storage.HeapSlotDead)
			if err != nil {
				t.Fatal(err)
			}
			header := c.Headers.Header(1)
			if header.Slot != state || header.Repaired || c.Retained != nil || c.Removed != 1 {
				t.Fatal("wrong genuine retirement", c, header)
			}
			if state == storage.HeapSlotRedirect && header.Redirect != 2 {
				t.Fatal("redirect did not name real successor", header)
			}
			if _, exists := c.Headers.Versions.Get(99); exists {
				t.Fatal("invented unallocated slot")
			}
			if store.Headers != capturedHeaders || len(store.Versions) != 1 || state == storage.HeapSlotRedirect && store.Headers.Header(1).Successor != 2 {
				t.Fatal("retirement mutated old roots")
			}
		})
	}
}

func TestPgsurgeryFreezeMaintenanceLaterEnd(t *testing.T) {
	ctx := context.Background()
	row := storage.RowVersion{Row: 1, Version: 1, Born: 1, InsertFullXID: 3}
	headers, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{row})
	if err != nil {
		t.Fatal(err)
	}
	captured := headers.Apply(row)
	ended := storage.CommitHeapEnds(headers, []storage.RowVersion{captured}, 7, 3)
	store := storage.Relation{Versions: []storage.RowVersion{row}, Headers: ended}
	rows := storage.EffectiveHeapRows(store.Versions, ended)
	c, err := storage.InspectVacuum(ctx, rows, nil, 3, 3, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err = storage.MaterializeVacuum(ctx, rows, nil, 3, 3, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err = storage.MaintainHeapHistory(ctx, c, store, 3, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Versions) != 0 || len(c.Headers.Additional) != 0 || c.Headers.Header(1).Slot != storage.HeapSlotUnused || c.Removed != 1 {
		t.Fatal("later genuine end did not retire repaired slot", c)
	}
	if got := c.Headers.Header(row.Version); got.TupleLockVersion != 0 || got.TupleLockStamp != 0 {
		t.Fatal("genuine retirement retained tuple anchor", got)
	}
	if headers.Apply(row).ForcedEnd != 0 || headers.Header(1).Slot != storage.HeapSlotNormal || len(headers.Additional) != 1 {
		t.Fatal("old repair capture changed")
	}
}

func TestPgsurgeryFreezeMaintenanceSuccessor(t *testing.T) {
	ctx := context.Background()
	a := storage.RowVersion{Row: 7, Version: 1, Born: 1, InsertFullXID: 3}
	b := storage.RowVersion{Row: 7, Version: 2, Born: 2, InsertFullXID: 4}
	c := storage.RowVersion{Row: 7, Version: 3, Born: 3, InsertFullXID: 5}
	updateB, updateC := b, c
	updateB.PrivateHistory = storage.RetainHeapPredecessor(a)
	updateC.PrivateHistory = storage.RetainHeapPredecessor(b)
	headers := storage.ReplaceHeapHeaders(nil, []storage.RowVersion{updateB}, 4)
	headers = storage.ReplaceHeapHeaders(headers, []storage.RowVersion{updateC}, 5)
	priorA := storage.RetainHeapPredecessor(a)
	priorA.End, priorA.EndFullXID = 2, 4
	priorB := storage.RetainHeapPredecessor(b)
	priorB.End, priorB.EndFullXID, priorB.Next = 3, 5, priorA
	source := storage.Relation{Versions: []storage.RowVersion{c}, Retained: priorB, Headers: headers}
	candidate, err := storage.InspectVacuum(ctx, source.Versions, nil, 3, 3, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	pruned, err := storage.MaintainHeapHistory(ctx, candidate, source, 3, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if pruned.Headers.Header(1).Redirect != 3 || pruned.Headers.Header(2).Redirect != 3 || pruned.Retained != nil {
		t.Fatal("pruning lost actual multihop UPDATE lineage", pruned)
	}
	if headers.Header(1).Successor != 2 || headers.Header(2).Successor != 3 || priorB.Next != priorA {
		t.Fatal("pruning mutated captured successor roots")
	}
	// Repair clears A's old successor. A later genuine DELETE can retire A,
	// but the still-visible same-RowID C is not new evidence of an A->C link.
	repaired, err := storage.RepairHeapHeaders(headers, []storage.RowVersion{a})
	if err != nil {
		t.Fatal(err)
	}
	deleted := storage.CommitHeapEnds(repaired, []storage.RowVersion{repaired.Apply(a)}, 6, 4)
	source.Headers = deleted
	pruned, err = storage.MaintainHeapHistory(ctx, candidate, source, 4, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if pruned.Headers.Header(1).Slot != storage.HeapSlotUnused || pruned.Headers.Header(1).Redirect != 0 {
		t.Fatal("RowID equality fabricated cleared UPDATE lineage")
	}
	if repaired.Header(1).Successor != 0 || repaired.Header(1).End != 0 || headers.Header(1).Successor != 2 {
		t.Fatal("later end changed old immutable repair/link roots")
	}
}
