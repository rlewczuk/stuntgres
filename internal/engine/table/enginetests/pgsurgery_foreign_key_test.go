package enginetests_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
)

func TestPgsurgeryFreezeForeignKeyRows(t *testing.T) {
	a := storage.RowVersion{Row: 1, Version: 10, Born: 1, InsertFullXID: 3}
	b := storage.RowVersion{Row: 1, Version: 20, Born: 2, InsertFullXID: 4}
	z := storage.RowVersion{Row: 2, Version: 30, Born: 1, InsertFullXID: 3}
	headers, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{a, b})
	if err != nil {
		t.Fatal(err)
	}
	// The private chain really replaced A at its repaired header, not B even
	// though both share RowID. Its Base names an older genuine predecessor.
	base := storage.RowVersion{Row: 1, Version: 5, Born: 1}
	old := headers.Apply(a)
	old.PrivateHistory = storage.RetainHeapPredecessor(base)
	own := storage.RowVersion{Row: 1, Version: 40, Base: 5, PrivateHistory: storage.RetainHeapPredecessor(old)}
	staged := storage.RowVersion{Row: 1, Version: 50, Base: 5, PrivateHistory: storage.RetainHeapPredecessor(own)}
	rows := table.ForeignKeyRows{Committed: []storage.RowVersion{a, b, z}, Own: []storage.RowVersion{own}, Staged: []storage.RowVersion{staged}, Headers: headers, Frontier: 3}
	rows.CapturePrivateShadows()
	if got := foreignKeyVersions(t, rows, false); !slices.Equal(got, []ids.VersionID{20, 30, 50}) {
		t.Fatal("private history suppressed an unrelated sibling or duplicated a version", got)
	}
	if got := foreignKeyVersions(t, rows, true); !slices.Equal(got, []ids.VersionID{50}) {
		t.Fatal("private index overlay included repaired heap candidates", got)
	}
	if rows.CommittedLive(a) || !rows.CommittedLive(b) {
		t.Fatal("indexed liveness did not use exact effective headers")
	}
	previous, ok := rows.Previous(a.Row, a.Version)
	if !ok || previous.Version != a.Version || !previous.ForcedFrozen {
		t.Fatal("exact physical predecessor was not found", previous, ok)
	}
	previous, ok = rows.Previous(b.Row, b.Version)
	if !ok || previous.Version != b.Version {
		t.Fatal("same-RowID predecessor lookup retargeted a sibling", previous, ok)
	}
	if _, ok := rows.Previous(99, a.Version); ok {
		t.Fatal("version lookup ignored RowID")
	}
	if _, ok := rows.Previous(a.Row, 999); ok {
		t.Fatal("missing exact version borrowed a same-RowID predecessor")
	}
	// Repair after the private admission clears only A's older shadow identity.
	fresh, err := storage.RepairHeapHeaders(headers, []storage.RowVersion{a})
	if err != nil {
		t.Fatal(err)
	}
	current := rows
	current.Headers = fresh
	current.CapturePrivateShadows()
	if got := foreignKeyVersions(t, current, false); !slices.Equal(got, []ids.VersionID{10, 20, 30, 50}) {
		t.Fatal("stale admission suppressed a freshly repaired predecessor", got)
	}
	if got := foreignKeyVersions(t, rows, false); !slices.Equal(got, []ids.VersionID{20, 30, 50}) {
		t.Fatal("new capture mutated old immutable shadow/header roots", got)
	}
	if a.ForcedFrozen || b.ForcedFrozen || headers.Apply(a).RepairStamp == fresh.Apply(a).RepairStamp {
		t.Fatal("repair changed borrowed rows or reused admission identity")
	}
	t.Run("private_siblings", func(t *testing.T) {
		private := storage.RowVersion{Row: 1, Version: 60}
		privateHeaders, err := storage.RepairHeapHeaders(headers, []storage.RowVersion{private})
		if err != nil {
			t.Fatal(err)
		}
		replacement := storage.RowVersion{Row: 1, Version: 70, Base: a.Version, PrivateHistory: storage.RetainHeapPredecessor(privateHeaders.Apply(a))}
		capture := table.ForeignKeyRows{Committed: []storage.RowVersion{a}, Own: []storage.RowVersion{private}, Staged: []storage.RowVersion{replacement}, Headers: privateHeaders, Frontier: 3}
		capture.CapturePrivateShadows()
		if got := foreignKeyVersions(t, capture, true); !slices.Equal(got, []ids.VersionID{60, 70}) {
			t.Fatal("staged predecessor shadowed a different private sibling", got)
		}
		if got := foreignKeyVersions(t, capture, false); !slices.Equal(got, []ids.VersionID{60, 70, 20}) {
			t.Fatal("private and additional repaired siblings were duplicated or omitted", got)
		}
		visits := 0
		if err := capture.Each(func(storage.RowVersion) (bool, error) { visits++; return true, nil }); err != nil || visits != 1 {
			t.Fatal("private successful stop continued into later roots", visits, err)
		}
		prior := &visibility.PrivateDeletes{}
		prior.Set(visibility.DeleteCandidate{Row: private.Row, Version: private.Version, RepairStamp: privateHeaders.Apply(private).RepairStamp})
		deleted := capture
		deleted.PriorDeletes = prior
		if got := foreignKeyVersions(t, deleted, true); !slices.Equal(got, []ids.VersionID{70}) {
			t.Fatal("private deletion suppressed an unrelated staged sibling", got)
		}
		again, err := storage.RepairHeapHeaders(privateHeaders, []storage.RowVersion{private})
		if err != nil {
			t.Fatal(err)
		}
		deleted.Headers = again
		deleted.CapturePrivateShadows()
		if got := foreignKeyVersions(t, deleted, true); !slices.Equal(got, []ids.VersionID{60, 70}) {
			t.Fatal("older private deletion suppressed a freshly repaired own version", got)
		}
		if got := foreignKeyVersions(t, capture, true); !slices.Equal(got, []ids.VersionID{60, 70}) {
			t.Fatal("repair/deletion changed captured private roots", got)
		}
	})
}

func TestPgsurgeryFreezeForeignKeyAdditional(t *testing.T) {
	a := storage.RowVersion{Row: 1, Version: 10, Born: 1}
	b := storage.RowVersion{Row: 1, Version: 20, Born: 2}
	headers, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{a, b})
	if err != nil {
		t.Fatal(err)
	}
	rows := table.ForeignKeyRows{Committed: []storage.RowVersion{b}, Headers: headers, Frontier: 3}
	if got := foreignKeyVersions(t, rows, false); !slices.Equal(got, []ids.VersionID{20, 10}) {
		t.Fatal("canonical additional version omitted or current head duplicated", got)
	}
	if got := foreignKeyVersions(t, rows, true); len(got) != 0 {
		t.Fatal("additional heaps invented a private index overlay", got)
	}
	deletes := &visibility.PrivateDeletes{}
	deletes.Set(visibility.DeleteCandidate{Row: 1, Version: 10, RepairStamp: headers.Apply(a).RepairStamp})
	deleted := rows
	deleted.CurrentDeletes = deletes
	if got := foreignKeyVersions(t, deleted, false); !slices.Equal(got, []ids.VersionID{20}) {
		t.Fatal("exact additional deletion affected its sibling", got)
	}
	ended := rows
	ended.Headers = storage.CommitHeapEnds(headers, []storage.RowVersion{headers.Apply(a)}, 9, 4)
	ended.Frontier = 4
	if got := foreignKeyVersions(t, ended, false); !slices.Equal(got, []ids.VersionID{20}) {
		t.Fatal("committed additional end was ignored", got)
	}
	if got := foreignKeyVersions(t, rows, false); !slices.Equal(got, []ids.VersionID{20, 10}) {
		t.Fatal("later end changed an old capture", got)
	}
	failure := errors.New("visitor failure")
	if err := rows.Each(func(storage.RowVersion) (bool, error) { return false, failure }); !errors.Is(err, failure) {
		t.Fatal("visitor failure was lost", err)
	}
	visits := 0
	if err := rows.Each(func(storage.RowVersion) (bool, error) { visits++; return true, nil }); err != nil || visits != 1 {
		t.Fatal("successful stop continued into additional versions", visits, err)
	}
	plain := table.ForeignKeyRows{Committed: []storage.RowVersion{b}, Frontier: 3}
	if allocs := testing.AllocsPerRun(20, func() {
		if err := plain.Each(func(storage.RowVersion) (bool, error) { return false, nil }); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatal("ordinary immutable enumeration allocated", allocs)
	}
}

func foreignKeyVersions(t *testing.T, rows table.ForeignKeyRows, private bool) []ids.VersionID {
	t.Helper()
	var result []ids.VersionID
	visit := func(row storage.RowVersion) (bool, error) {
		result = append(result, row.Version)
		return false, nil
	}
	var err error
	if private {
		err = rows.EachPrivate(visit)
	} else {
		err = rows.Each(visit)
	}
	if err != nil {
		t.Fatal(err)
	}
	return result
}
