package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

// Exercise the owner seam synchronously: real committed versions and a real
// admitted command, with a peer commit between snapshot selection and first
// target-root capture. No timing, row-wait probe, or replacement provider is used.
func TestSnapshotStatementRootConsistency(t *testing.T) {
	ctx := context.Background()
	e, seed, read, relation, _ := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	integer, _ := read.LookupType(sqltype.Int4)
	triggerUnitAdmit(t, seed, relation)
	values := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 10)}
	if count, err := dml.ApplyInsertValidated(seed.WriteHost(), seed.LockedRef, index.WriteBatch{Values: [][]sqltype.Value{values}}); err != nil || count != 1 {
		t.Fatal("seed real committed predecessor", count, err)
	}
	if err := seed.Finish(nil); err != nil {
		t.Fatal(err)
	}

	op, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	writer, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Finish(nil)
	update := statement(t, "UPDATE public."+relation.Name()+" SET v=v+1 WHERE id=1")
	if err := writer.Gate(update); err != nil {
		t.Fatal(err)
	}
	if err := writer.AcquireRelation(update, true); err != nil {
		t.Fatal(err)
	}
	if err := ScopeSnapshotPhase(writer, update); err != nil {
		t.Fatal(err)
	}
	snapshot, frontier := ScopeCurrentSnapshot(writer), ScopeSnapshotFrontier(writer)
	if snapshot == nil {
		t.Fatal("admitted writer has no command snapshot")
	}
	e.Mu.Lock()
	predecessor := e.Storage.Relations[relation.Address()].Versions[0]
	e.Mu.Unlock()

	// A separate real operation publishes the successor before this command
	// has acquired any target data view. This fixes the failing interleaving.
	peer, err := testExecute(e, ctx, statement(t, "UPDATE public."+relation.Name()+" SET v=20 WHERE id=1"), nil)
	if err != nil || peer.Count != 1 {
		t.Fatal("peer replacement commit", peer.Count, err)
	}
	e.Mu.Lock()
	successor := e.Storage.Relations[relation.Address()].Versions[0]
	e.Mu.Unlock()
	if successor.Row != predecessor.Row || successor.Version == predecessor.Version || successor.Born <= frontier {
		t.Fatal("peer did not publish a new committed version of the same row after the selected frontier")
	}
	view, err := writer.AcquireDataView(writer.LockedRef)
	if err != nil {
		t.Fatal(err)
	}
	source, err := exec.CaptureSource(ctx, view, writer.LockedRef)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Count() != 1 {
		t.Fatal("selected snapshot lost its precommit candidate during delayed root capture", source.Count())
	}
	captured := source.VersionAt(source.RawIndex(0))
	if captured.Row != predecessor.Row || captured.Version != predecessor.Version || !sqltype.SameDatum(captured.Values[1], values[1]) {
		t.Fatal("captured candidate is not the original snapshot version")
	}
	if ScopeCurrentSnapshot(writer) != snapshot || ScopeSnapshotFrontier(writer) != frontier {
		t.Fatal("root capture refreshed the command snapshot instead of retaining its roots")
	}

	// Execute against that frozen candidate through the actual command owner.
	// The incumbent row-lock/latest-version seam must use peer's 20, not 10.
	result, err := EngineExecuteStatement(e, writer, update, 0, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
		return nil
	})
	if err != nil || result.Count != 1 {
		t.Fatal("latest-version arbitration must update exactly once", result.Count, err)
	}
	if err := writer.Finish(nil); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	committed := e.Storage.Relations[relation.Address()].Versions[0]
	e.Mu.Unlock()
	want := triggerUnitInt(t, integer, 21)
	if committed.Row != predecessor.Row || !sqltype.SameDatum(committed.Values[1], want) {
		t.Fatal("writer did not retain logical identity and apply SET to the committed latest predecessor")
	}
}
