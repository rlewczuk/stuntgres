package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func TestDomainCheckIndexSnapshotRejectsMissingAndMalformedRoots(t *testing.T) {
	f := newPartialIndexFixture(t)
	ctx := context.Background()
	if _, err := core.EngineCaptureIndexSnapshot(f.e, ctx, f.e.CatalogRead(), f.e.Storage.Relations, nil, f.e.Visibility.CommitSeq()); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("missing root accepted", err)
	}
	original := f.e.Storage.IndexRoots[f.index.Address()]
	malformed := *original
	malformed.Entries = slices.Clone(original.Entries)
	malformed.Entries[0].Position = len(f.store.Versions)
	roots := map[ids.ObjectAddress]*storage.IndexRoot{f.index.Address(): &malformed}
	previous := f.e.Storage.IndexRoots
	f.e.Mu.Lock()
	f.e.Storage.ReplaceIndexRootsLocked(roots)
	f.e.Mu.Unlock()
	if _, err := core.EngineCaptureIndexSnapshot(f.e, ctx, f.e.CatalogRead(), f.e.Storage.Relations, roots, f.e.Visibility.CommitSeq()); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("malformed source provenance accepted", err)
	}
	f.e.Mu.Lock()
	f.e.Storage.ReplaceIndexRootsLocked(previous)
	f.e.Mu.Unlock()
	if original.Entries[0].Position == malformed.Entries[0].Position {
		t.Fatal("rejection mutated source")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := core.EngineCaptureIndexSnapshot(f.e, canceled, f.e.CatalogRead(), f.e.Storage.Relations, f.e.Storage.IndexRoots, f.e.Visibility.CommitSeq()); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}
