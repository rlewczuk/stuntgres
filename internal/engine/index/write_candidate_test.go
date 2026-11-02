package index

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestWriteBatchAcceptedVersions(t *testing.T) {
	// Caller-owned physical output follows the same windows as ordinary values,
	// including zero-column rows. No vector is created for an undemanded output.
	var plain WriteBatch
	plain.Append(WriteCandidate{})
	if plain.Accepted != nil || len(plain.Values) != 1 {
		t.Fatal("plain write acquired physical output storage")
	}
	accepted := make([]storage.RowVersion, 0, 3)
	batch := WriteBatch{Accepted: accepted}
	for range 3 {
		batch.Append(WriteCandidate{Values: []sqltype.Value{}})
	}
	for i := range batch.Accepted {
		batch.Accepted[i] = storage.RowVersion{Row: 7, Version: ids.VersionID(i + 1)}
	}
	prefix := batch.Prefix(2)
	window := batch.Slice(1, 3)
	if len(prefix.Accepted) != 2 || len(window.Accepted) != 2 || &prefix.Accepted[0] != &batch.Accepted[0] || &window.Accepted[0] != &batch.Accepted[1] {
		t.Fatal("accepted windows copied or lost physical coordinates")
	}
	window.Set(0, WriteCandidate{})
	if batch.Accepted[1].Version != 0 || batch.Accepted[0].Version != 1 || batch.Accepted[2].Version != 3 {
		t.Fatal("replacement retained stale acceptance or erased another slot", batch.Accepted)
	}
}

func TestWriteBatchPredecessorHint(t *testing.T) {
	var plain WriteBatch
	plain.Append(WriteCandidate{})
	if plain.PredecessorHintKnown {
		t.Fatal("zero-value batch invented a known predecessor ordinal")
	}
	batch := WriteBatch{
		Values:               make([][]sqltype.Value, 1, 2),
		Accepted:             make([]storage.RowVersion, 1, 2),
		PredecessorHint:      9,
		PredecessorHintKnown: true,
	}
	for _, window := range []WriteBatch{batch.Prefix(1), batch.Slice(0, 1)} {
		if !window.PredecessorHintKnown || window.PredecessorHint != 9 || &window.Values[0] != &batch.Values[0] || &window.Accepted[0] != &batch.Accepted[0] {
			t.Fatal("single-row window lost its borrowed predecessor position")
		}
		window.Set(0, WriteCandidate{})
		if !window.PredecessorHintKnown || window.PredecessorHint != 9 {
			t.Fatal("replacement payload update changed predecessor identity")
		}
	}
	zero := batch
	zero.PredecessorHint = 0
	if window := zero.Prefix(1).Slice(0, 1); !window.PredecessorHintKnown || window.PredecessorHint != 0 {
		t.Fatal("genuine first-slot hint became unknown")
	}
	for _, window := range []WriteBatch{batch.Prefix(0), batch.Slice(1, 1)} {
		if window.PredecessorHintKnown {
			t.Fatal("empty window retained another row's position")
		}
	}
	batch.Append(WriteCandidate{})
	if batch.PredecessorHintKnown {
		t.Fatal("multirow batch retained a single-row hint")
	}
	// Even a caller-supplied scalar cannot become authority for an arbitrary
	// row sliced from a multirow payload.
	batch.PredecessorHint, batch.PredecessorHintKnown = 9, true
	for _, window := range []WriteBatch{batch.Prefix(1), batch.Slice(0, 1), batch.Slice(1, 2)} {
		if window.PredecessorHintKnown {
			t.Fatal("multirow window guessed a predecessor ordinal")
		}
	}
	values := make([][]sqltype.Value, 1)
	allocations := testing.AllocsPerRun(100, func() {
		single := WriteBatch{Values: values, PredecessorHint: 9, PredecessorHintKnown: true}
		window := single.Prefix(1).Slice(0, 1)
		if !window.PredecessorHintKnown || window.PredecessorHint != 9 {
			t.Fatal("single-row position was not retained")
		}
	})
	if allocations != 0 {
		t.Fatal("single-row hint windows allocated", allocations)
	}
}
