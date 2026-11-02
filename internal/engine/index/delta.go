package index

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// collectIndexDelta evaluates only newly admitted rows, preferring actual
// produced keys. Ended identities and key outputs are collected once per batch.
func collectIndexDelta(evaluation *exec.Context, parent, index *catalog.Relation, p *RowPublication, reconstruct bool, o *Ordering) ([]storage.IndexEntry, map[ids.RowID]ids.VersionID, error) {
	ctx := evaluation.Context
	d := index.SQLIndex
	count := len(p.Writes)
	if p.Deletion != nil {
		count += len(p.Deletion.Rows)
	}
	ended := make(map[ids.RowID]ids.VersionID, count)
	added := make([]storage.IndexEntry, 0, len(p.Writes))
	var preparation ExecutionPreparation
	for i, row := range p.Writes {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if row.Base != 0 {
			ended[row.Row] = row.Base
		}
		if p.Deletion != nil {
			if end, ok := p.Deletion.Rows[row.Row]; ok && end.Version == row.Version {
				continue
			}
		}
		output, produced := row.IndexOutputs[index.Address()]
		produced = produced && OutputMatches(output, index)
		if !reconstruct && !produced && !RowMaintainsIndex(row, index) {
			continue
		}
		var entry storage.IndexEntry
		if produced {
			if !output.Member {
				continue
			}
			entry = storage.IndexEntry{Keys: output.Keys, Row: row.Row, Version: row.Version}
		} else {
			if d.HasRuntimeDomainConversion() && evaluation.LiveHost() == nil && row.Born == 0 {
				return nil, nil, fmt.Errorf("stuntgres: missing produced index row")
			}
			matches, err := preparation.PredicateMatches(evaluation, parent, d, row.Values, o, nil)
			if err != nil {
				return nil, nil, err
			}
			if !matches {
				continue
			}
			if err := preparation.PrepareKeys(evaluation, parent, d, o, nil); err != nil {
				return nil, nil, err
			}
			entry, err = o.Entry(row, evaluation)
			if err != nil {
				return nil, nil, err
			}
		}
		entry.Position = PublicationSlot(p, i)
		added = append(added, entry)
	}
	if p.Deletion != nil {
		for id, end := range p.Deletion.Rows {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if !end.Own {
				ended[id] = end.Version
			}
		}
	}
	return added, ended, ctx.Err()
}
