package index

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/storage"
)

// RebuildBrinVacuumRoot regroups conservative produced summaries against actual
// destination slots. VACUUM never reevaluates expression/predicate/domain truth.
// A contributor without summary coverage makes its destination unsummarized.
func RebuildBrinVacuumRoot(ctx context.Context, index *catalog.Relation, old *storage.IndexRoot, rows []storage.RowVersion, c storage.VacuumCandidate, store *storage.Relation, generation uint64) (*storage.IndexRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if index == nil || index.SQLIndex == nil || index.SQLIndex.Method != catalog.BrinMethod || old == nil || old.Brin == nil {
		return nil, fmt.Errorf("stuntgres: invalid BRIN vacuum capture")
	}
	source := old.Brin
	next := *source
	next.Storage, next.Generation, next.Prefix, next.Ends = store, generation, len(c.Versions), c.Ends
	if c.Positions == nil {
		return &storage.IndexRoot{Brin: &next}, nil
	}
	if len(c.Positions) != len(rows) || source.EffectivePagesPerRange < 1 {
		return nil, fmt.Errorf("stuntgres: invalid BRIN vacuum positions")
	}
	width, _ := BrinOptions(index)
	next.EffectivePagesPerRange = width
	next.Ranges = make([]storage.BrinRange, (len(c.Versions)+width-1)/width)
	for i := range next.Ranges {
		next.Ranges[i] = storage.BrinRange{Summarized: true, Keys: make([]storage.BrinSummary, len(source.Bindings))}
	}
	seen := make(map[[2]int]bool)
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
	for before, after := range c.Positions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if after < 0 {
			continue
		}
		if after >= len(c.Versions) || rows[before].Row != c.Versions[after].Row || rows[before].Version != c.Versions[after].Version {
			return nil, fmt.Errorf("stuntgres: invalid BRIN vacuum row identity")
		}
		from, to := before/source.EffectivePagesPerRange, after/width
		destination := &next.Ranges[to]
		if !destination.Summarized {
			continue
		}
		if before >= source.Prefix || from >= len(source.Ranges) || !source.Ranges[from].Summarized {
			*destination = storage.BrinRange{}
			continue
		}
		pair := [2]int{from, to}
		if seen[pair] {
			continue
		}
		seen[pair] = true
		for key, s := range source.Ranges[from].Keys {
			d := &destination.Keys[key]
			d.HasNull = d.HasNull || s.HasNull
			if !s.HasNonNull {
				continue
			}
			if err := extendBrinSummary(evaluation, d, source.Bindings[key], s.Min); err != nil {
				return nil, err
			}
			if err := extendBrinSummary(evaluation, d, source.Bindings[key], s.Max); err != nil {
				return nil, err
			}
		}
	}
	return &storage.IndexRoot{Brin: &next}, ctx.Err()
}
