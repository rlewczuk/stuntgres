package enginetests_test

import (
	"context"
	"errors"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"testing"
)

func ginSplitDictionaryFixture(t *testing.T, tokens int) ginFixture {
	t.Helper()
	f := ginDictionaryFixture(t, tokens)
	entry := f.root.Entries[0]
	entry.Row, entry.Version, entry.Position = 2, 2, 1
	payload, err := storage.MergeGin(context.Background(), f.root.Gin, nil, []storage.IndexEntry{entry}, nil, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	root := *f.root
	root.Gin = payload
	root.Entries = append(slices.Clone(root.Entries), entry)
	f.root = &root
	row := f.store.Versions[0]
	row.Row, row.Version = 2, 2
	f.store.Versions = append(slices.Clone(f.store.Versions), row)
	return f
}
func ginMaintenanceNonmaterializing(t *testing.T) {
	small, large := ginSplitDictionaryFixture(t, 8), ginSplitDictionaryFixture(t, 8192)
	for _, pending := range []bool{false, true} {
		measure := func(f ginFixture, deleteOnly bool) int64 {
			p := ginAdmissionDelta(t, f)
			p.Writes[0].IndexOutputs = map[ids.ObjectAddress]storage.IndexOutput{f.descriptor.Address(): {Index: f.descriptor, Member: true, Keys: p.Writes[0].Values, GinPending: pending}}
			if deleteOnly {
				p.Writes = nil
				p.Deletion = &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{2: {Row: 2, Version: 2}}}
			}
			before := ginCopyPayload(f.root.Gin)
			var candidate *storage.IndexRoot
			var err error
			_, bytes := util.AllocationsPerCall(3, func() {
				candidate, err = index.MergeGinIndexRoot(f.evaluation, f.parent, f.descriptor, f.root, p, false, false)
			})
			if err != nil || candidate != nil {
				t.Fatal("split admission", pending, deleteOnly, err)
			}
			if storage.GinPendingIncidences(f.root.Gin) != int64(len(before.Pending[0].Tokens)+1) {
				t.Fatal("split predecessor mutated")
			}
			return bytes
		}
		for _, deleteOnly := range []bool{false, true} {
			smallBytes, largeBytes := measure(small, deleteOnly), measure(large, deleteOnly)
			if largeBytes > smallBytes+32*1024 {
				t.Fatal("split validation allocated incumbent output", pending, deleteOnly, smallBytes, largeBytes)
			}
		}
	}
	p := ginAdmissionDelta(t, large)
	p.Writes[0].IndexOutputs = map[ids.ObjectAddress]storage.IndexOutput{large.descriptor.Address(): {Index: large.descriptor, Member: true, Keys: p.Writes[0].Values, GinPending: true}}
	foreign := newGinFixture(t)
	damaged := *large.root
	damaged.Gin = ginCopyPayload(large.root.Gin)
	last := len(damaged.Gin.Pending[0].Tokens) - 1
	damaged.Gin.Pending[0].Tokens[last].Token.Element = ginInput(t, foreign.parent.Columns[0].Usage.Type, "{8191}").ArrayElement(0)
	if candidate, err := index.MergeGinIndexRoot(large.evaluation, large.parent, large.descriptor, &damaged, p, false, false); candidate != nil || err == nil {
		t.Fatal("pending comparison skipped")
	}
	for _, polls := range []int{1, 10, 1000} {
		evaluation := *large.evaluation
		ctx := &spgistInterruptContext{Context: context.Background(), remaining: polls}
		evaluation.Context = ctx
		if candidate, err := index.MergeGinIndexRoot(&evaluation, large.parent, large.descriptor, large.root, p, false, false); candidate != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("pending validation cancellation", polls, err)
		}
	}
}
