package storage

import (
	"context"
	"fmt"
	"math"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Allocation borrows immutable vectors captured together under the engine mutex.
// It never retains the mutable Relation header or copies datum payloads.
type Allocation struct {
	Relation *catalog.Relation
	Allocated bool
	Versions []RowVersion
	Own []RowVersion
	Staged []RowVersion
	ReplaceOwn bool
	Index *IndexRoot
	Parent *catalog.Relation
	ParentVersions []RowVersion
	ParentOwn []RowVersion
	ParentStaged []RowVersion
}

// ForkSizes are logical allocation estimates, not PostgreSQL file byte counts.
type ForkSizes struct { Main, FSM, VM, Init int64 }

func AddSize(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "bigint out of range"}
	}
	return a+b, nil
}

func (s ForkSizes) Total() (int64, error) {
	n, err := AddSize(s.Main, s.FSM)
	if err != nil { return 0, err }
	n, err = AddSize(n, s.VM)
	if err != nil { return 0, err }
	return AddSize(n, s.Init)
}

func cellSize(value sqltype.Value) int64 {
	if value.IsNull() { return 0 }
	if n := int64(value.Type().PostgreSQLTypeLength()); n > 0 { return (n+7)/8*8 }
	return 32
}

func valuesSize(ctx context.Context, values []sqltype.Value, base int64) (int64, error) {
	for i, value := range values {
		if i&255 == 0 { if err := ctx.Err(); err != nil { return 0, err } }
		var err error
		base, err = AddSize(base, cellSize(value))
		if err != nil { return 0, err }
	}
	return base, nil
}

// RowAllocation includes a 24-byte header, 8-aligned null bitmap and datum slots.
func RowAllocation(ctx context.Context, values []sqltype.Value) (int64, error) {
	bitmap := (int64(len(values))+63)/64*8
	base, err := AddSize(24, bitmap)
	if err != nil { return 0, err }
	return valuesSize(ctx, values, base)
}

func (a Allocation) Forks(ctx context.Context) (ForkSizes, error) {
	if err := ctx.Err(); err != nil { return ForkSizes{}, err }
	if !a.Allocated { return ForkSizes{}, nil }
	if a.Relation == nil { return ForkSizes{}, fmt.Errorf("stuntgres: allocation without relation authority") }
	if a.Relation.SQLIndex != nil { return a.indexForks(ctx) }
	if a.Relation.Sequence != nil {
		s := ForkSizes{Main: 64}
		if a.Relation.Sequence.Persistence == "unlogged" { s.Init = 64 }
		return s, nil
	}
	s := ForkSizes{Main: 64}
	staged, err := allocationStagedRows(ctx, a.Own, a.Staged)
	if err != nil { return ForkSizes{}, err }
	var n int64
	for source, rows := range [3][]RowVersion{a.Versions, a.Own, a.Staged} {
		for i, row := range rows {
			if i&255 == 0 { if err := ctx.Err(); err != nil { return ForkSizes{}, err } }
			if source == 1 {
				if version, found := staged[row.Row]; found && (a.ReplaceOwn || version == row.Version) { continue }
			}
			size, err := RowAllocation(ctx, row.Values)
			if err != nil { return ForkSizes{}, err }
			s.Main, err = AddSize(s.Main, size)
			if err != nil { return ForkSizes{}, err }
			n, err = AddSize(n, 1)
			if err != nil { return ForkSizes{}, err }
		}
	}
	s.FSM = (n/256)*8
	if n%256 != 0 { s.FSM += 8 }
	s.VM = n/8
	if n%8 != 0 { s.VM++ }
	return s, nil
}

func (a Allocation) entrySize(ctx context.Context, entry IndexEntry) (int64, error) {
	n, err := valuesSize(ctx, entry.Keys, 16)
	if err != nil || len(a.Relation.SQLIndex.Include) == 0 { return n, err }
	row, found, err := allocationEntryRow(ctx, a.ParentVersions, entry)
	if err != nil { return 0, err }
	if !found { row, found, err = allocationEntryRow(ctx, a.ParentOwn, entry) }
	if err != nil { return 0, err }
	if !found { row, found, err = allocationEntryRow(ctx, a.ParentStaged, entry) }
	if err != nil { return 0, err }
	if !found { return 0, fmt.Errorf("stuntgres: index allocation lost included row identity") }
	return a.includedSize(ctx, row.Values, n)
}

// Private entries already own the exact included row; only captured index
// entries need the identity lookup above.
func (a Allocation) includedSize(ctx context.Context, values []sqltype.Value, n int64) (int64, error) {
	if len(a.Relation.SQLIndex.Include) == 0 { return n, nil }
	if a.Parent == nil { return 0, fmt.Errorf("stuntgres: index allocation lost included parent") }
	for _, attribute := range a.Relation.SQLIndex.Include {
		if err := ctx.Err(); err != nil { return 0, err }
		slot := -1
		for i, number := range a.Parent.Attributes { if number == attribute { slot = i; break } }
		if slot < 0 || slot >= len(values) { return 0, fmt.Errorf("stuntgres: index allocation lost included attribute") }
		var err error
		n, err = AddSize(n, cellSize(values[slot]))
		if err != nil { return 0, err }
	}
	return n, nil
}

func (a Allocation) indexForks(ctx context.Context) (ForkSizes, error) {
	if a.Index == nil { return ForkSizes{}, nil }
	s := ForkSizes{Main: 64}
	add := func(n int64, err error) error {
		if err != nil { return err }
		s.Main, err = AddSize(s.Main, n)
		return err
	}
	switch {
	case a.Index.Hash != nil:
		for _, bucket := range a.Index.Hash.Buckets {
			for _, entry := range bucket {
				if err := ctx.Err(); err != nil { return ForkSizes{}, err }
				if err := add(AddSize(16, cellSize(entry.Key))); err != nil { return ForkSizes{}, err }
			}
		}
	case a.Index.Brin != nil:
		for _, r := range a.Index.Brin.Ranges {
			if err := ctx.Err(); err != nil { return ForkSizes{}, err }
			if !r.Summarized { continue }
			n := int64(16)
			for _, key := range r.Keys {
				var err error
				n, err = AddSize(n, 8)
				if err != nil { return ForkSizes{}, err }
				if key.HasNonNull {
					n, err = AddSize(n, cellSize(key.Min)+cellSize(key.Max))
					if err != nil { return ForkSizes{}, err }
				}
			}
			if err := add(n, nil); err != nil { return ForkSizes{}, err }
		}
	default:
		for _, entry := range a.Index.Entries {
			if err := ctx.Err(); err != nil { return ForkSizes{}, err }
			if err := add(a.entrySize(ctx, entry)); err != nil { return ForkSizes{}, err }
		}
	}
	// Private DML owns produced memberships on rows; journal roots may
	// already contain them. BRIN's unsummarized suffix owns no summary.
	if a.Index.Brin == nil && (len(a.ParentOwn) != 0 || len(a.ParentStaged) != 0) {
		staged, err := allocationStagedRows(ctx, a.ParentOwn, a.ParentStaged)
		if err != nil { return ForkSizes{}, err }
		seen := make(map[ids.VersionID]bool)
		if a.Index.Hash != nil {
			for _, bucket := range a.Index.Hash.Buckets {
				for _, entry := range bucket { seen[entry.Version] = true }
			}
		} else {
			for _, entry := range a.Index.Entries { seen[entry.Version] = true }
		}
		for source, rows := range [2][]RowVersion{a.ParentOwn, a.ParentStaged} {
		for _, row := range rows {
			if source == 0 {
				if version, found := staged[row.Row]; found && (a.ReplaceOwn || version == row.Version) { continue }
			}
			if err := ctx.Err(); err != nil { return ForkSizes{}, err }
			if seen[row.Version] { continue }
			output, produced := row.IndexOutputs[a.Relation.Address()]
			if produced && (output.Index == nil || output.Index.Address() != a.Relation.Address()) {
				return ForkSizes{}, fmt.Errorf("stuntgres: foreign private index allocation output")
			}
			if produced && !output.Member { continue }
			if a.Relation.SQLIndex.Predicate != nil && !produced {
				return ForkSizes{}, fmt.Errorf("stuntgres: missing private index membership allocation")
			}
			n := int64(16)
			if produced {
				var err error
				if a.Index.Hash != nil { n, err = AddSize(n, cellSize(output.Key)) } else { n, err = valuesSize(ctx, output.Keys, n) }
				if err != nil { return ForkSizes{}, err }
			} else {
				for _, key := range a.Relation.SQLIndex.Keys {
					if key.Derived != nil || a.Parent == nil { return ForkSizes{}, fmt.Errorf("stuntgres: missing private index key allocation") }
					slot := -1
					for i, attribute := range a.Parent.Attributes { if attribute == key.Attribute { slot = i; break } }
					if slot < 0 || slot >= len(row.Values) { return ForkSizes{}, fmt.Errorf("stuntgres: missing private index key attribute") }
					var err error
					n, err = AddSize(n, cellSize(row.Values[slot]))
					if err != nil { return ForkSizes{}, err }
				}
			}
			n, err = a.includedSize(ctx, row.Values, n)
			if err != nil { return ForkSizes{}, err }
			if err := add(n, nil); err != nil { return ForkSizes{}, err }
		}
		}
	}
	return s, nil
}

func allocationEntryRow(ctx context.Context, rows []RowVersion, entry IndexEntry) (RowVersion, bool, error) {
	if err := ctx.Err(); err != nil { return RowVersion{}, false, err }
	if entry.Position >= 0 && entry.Position < len(rows) {
		row := rows[entry.Position]
		if row.Row == entry.Row && row.Version == entry.Version { return row, true, nil }
	}
	for i, row := range rows {
		if i&255 == 0 { if err := ctx.Err(); err != nil { return RowVersion{}, false, err } }
		if row.Row == entry.Row && row.Version == entry.Version { return row, true, nil }
	}
	return RowVersion{}, false, nil
}

// SQL routine activation can expose the same staged version in Own as well.
// Different versions supersede prior private rows only for replacement staging.
func allocationStagedRows(ctx context.Context, own, staged []RowVersion) (map[ids.RowID]ids.VersionID, error) {
	if len(own) == 0 || len(staged) == 0 { return nil, nil }
	rows := make(map[ids.RowID]ids.VersionID, len(staged))
	for i, row := range staged {
		if i&255 == 0 { if err := ctx.Err(); err != nil { return nil, err } }
		rows[row.Row] = row.Version
	}
	return rows, nil
}
