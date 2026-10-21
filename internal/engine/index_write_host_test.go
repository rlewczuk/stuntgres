package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Each projection change through the write host advances the exclusion
// registry generation exactly once, so a scan captured before it is stale and
// one captured after it is current.
func TestIndexWriteHostPublishExclusionAdvancesGeneration(t *testing.T) {
	e := New()
	sessionSQL(t, sessionStart(t, e, nil), "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING btree(k WITH =))")
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.finished {
			_ = scope.Finish(errors.New("unit rollback"))
		}
	})
	if err := scope.gate(&parser.Insert{}); err != nil {
		t.Fatal(err)
	}
	h := scope.writeHost()
	var _ index.WriteHost = h
	ref := roleTableRef(t, e, "public.t")
	binding, err := dml.BindUniqueWrite(scope.writeHost(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.Keys) != 1 || !binding.Keys[0].Index.SQLIndex.IsExclusion() {
		t.Fatalf("binding keys %d, want the one exclusion index", len(binding.Keys))
	}
	idx := binding.Keys[0].Index
	before, err := h.CaptureExclusionScan(ref, binding, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !h.ExclusionScanCurrent(before, idx.Address()) {
		t.Fatal("a fresh scan is not current")
	}
	value, err := sqltype.Integer(binding.Parent.Columns[0].Usage.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	key := []sqltype.Value{value}
	proposal := index.WriteCandidate{Values: []sqltype.Value{value}}
	row, err := h.PublishExclusion(ref, binding.Catalog, &proposal, index.ExclusionEdit{Index: idx.Address(), Keys: key, Member: true})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Exclusion == nil || proposal.Exclusion.Version != row.Version || row.Version == 0 {
		t.Fatalf("published row %+v, proposal exclusion %+v", row, proposal.Exclusion)
	}
	if h.ExclusionScanCurrent(before, idx.Address()) {
		t.Fatal("a scan captured before the publication is still current")
	}
	after, err := h.CaptureExclusionScan(ref, binding, idx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation+1 {
		t.Fatalf("generation %d after publication, want %d", after.Generation, before.Generation+1)
	}
	if !h.ExclusionScanCurrent(after, idx.Address()) {
		t.Fatal("a scan captured after the publication is not current")
	}
	if got := h.OwnExclusionKeys(idx.Address(), row.Version); len(got) != 1 || !sqltype.SameDatum(got[0], value) {
		t.Fatalf("own exclusion keys %v, want [7]", got)
	}
	h.DiscardExclusion(&proposal)
	if proposal.Exclusion != nil || h.OwnExclusionKeys(idx.Address(), row.Version) != nil {
		t.Fatal("discarded proposal is still projected")
	}
	if h.ExclusionScanCurrent(after, idx.Address()) {
		t.Fatal("a scan captured before the discard is still current")
	}
}
