package index

import (
	"context"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

type ginRetainedOccurrence struct {
	identity RowIdentity
	pending  bool
}
type ginRetainedValues struct {
	first    ginRetainedOccurrence
	repeated []ginRetainedOccurrence
	consumed int
}

func addGinRetained(rows map[*sqltype.Value]ginRetainedValues, row storage.RowVersion, pending bool) {
	key := &row.Values[0]
	group, exists := rows[key]
	identity := RowIdentity{Row: row.Row, Version: row.Version}
	if !exists {
		rows[key] = ginRetainedValues{first: ginRetainedOccurrence{identity, pending}}
		return
	}
	if group.repeated == nil && group.first.identity == identity {
		group.first.pending = group.first.pending && pending
		rows[key] = group
		return
	}
	if group.repeated == nil {
		group.repeated = []ginRetainedOccurrence{group.first}
	}
	for i := range group.repeated {
		if group.repeated[i].identity == identity {
			group.repeated[i].pending = group.repeated[i].pending && pending
			rows[key] = group
			return
		}
	}
	group.repeated = append(group.repeated, ginRetainedOccurrence{identity, pending})
	rows[key] = group
}

// TransportConcurrentGin preserves placement through the concurrent refresh
// owner's retained Values provenance. All keys/tokens have already been
// produced by its ordinary typed builder; this partitions postings only.
func TransportConcurrentGin(ctx context.Context, target *catalog.Relation, previous *storage.Relation, source, built *storage.IndexRoot, next *storage.Relation, own []storage.RowVersion, outputs *BuildCapture) (*storage.IndexRoot, error) {
	if previous == nil || next == nil || source == nil || source.Gin == nil || built == nil || built.Gin == nil || len(source.Gin.Columns) != len(built.Gin.Columns) || outputs != nil && len(outputs.Outputs) != len(next.Versions) {
		return nil, ids.ErrCatalogReplaced
	}
	for _, columns := range [2][]storage.GinColumn{source.Gin.Columns, source.Gin.Pending} {
		if len(columns) != 0 && len(columns) != len(built.Gin.Columns) {
			return nil, ids.ErrCatalogReplaced
		}
		for i, column := range columns {
			if !column.Binding.Same(built.Gin.Columns[i].Binding) {
				return nil, ids.ErrCatalogReplaced
			}
		}
	}
	oldPending := make(map[RowIdentity]bool)
	if len(source.Gin.Pending) != 0 {
		for _, token := range source.Gin.Pending[0].Tokens {
			for _, posting := range token.Postings {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				oldPending[RowIdentity{Row: posting.Row, Version: posting.Version}] = true
			}
		}
	}
	retained := make(map[*sqltype.Value]ginRetainedValues, len(source.Entries)+len(own))
	for _, entry := range source.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Position < 0 || entry.Position >= len(previous.Versions) {
			return nil, ids.ErrCatalogReplaced
		}
		row := previous.Versions[entry.Position]
		if row.Row != entry.Row || row.Version != entry.Version || len(row.Values) == 0 {
			return nil, ids.ErrCatalogReplaced
		}
		flag := oldPending[RowIdentity{Row: entry.Row, Version: entry.Version}]
		addGinRetained(retained, row, flag)
	}
	for _, row := range own {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if output, ok := row.IndexOutputs[target.Address()]; ok && output.Member && OutputMatches(output, target) && len(row.Values) != 0 {
			addGinRetained(retained, row, output.GinPending)
		}
	}
	// Equal images in the selected unique-key group are visited in exact old
	// row/version order. Keep each occurrence even when its Values backing is shared.
	for key, group := range retained {
		if len(group.repeated) == 0 {
			continue
		}
		slices.SortFunc(group.repeated, func(a, b ginRetainedOccurrence) int {
			if a.identity.Row < b.identity.Row {
				return -1
			}
			if a.identity.Row > b.identity.Row {
				return 1
			}
			if a.identity.Version < b.identity.Version {
				return -1
			}
			if a.identity.Version > b.identity.Version {
				return 1
			}
			return 0
		})
		retained[key] = group
	}
	pending := make(map[ids.VersionID]bool, len(built.Entries))
	fast := GinFastUpdate(target)
	for _, entry := range built.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Position < 0 || entry.Position >= len(next.Versions) {
			return nil, ids.ErrCatalogReplaced
		}
		row := next.Versions[entry.Position]
		if row.Row != entry.Row || row.Version != entry.Version || len(row.Values) == 0 {
			return nil, ids.ErrCatalogReplaced
		}
		flag := fast
		if group, matched := retained[&row.Values[0]]; matched {
			if len(group.repeated) == 0 {
				if group.consumed != 0 {
					return nil, ids.ErrCatalogReplaced
				}
				flag = group.first.pending
			} else {
				if group.consumed >= len(group.repeated) {
					return nil, ids.ErrCatalogReplaced
				}
				flag = group.repeated[group.consumed].pending
			}
			group.consumed++
			retained[&row.Values[0]] = group
		}
		pending[row.Version] = flag
	}
	main, err := storage.TransportGin(ctx, built.Gin, func(p storage.GinPosting) (storage.GinPosting, bool, error) { return p, !pending[p.Version], nil })
	if err != nil {
		return nil, err
	}
	deferred, err := storage.TransportGin(ctx, built.Gin, func(p storage.GinPosting) (storage.GinPosting, bool, error) { return p, pending[p.Version], nil })
	if err != nil {
		return nil, err
	}
	payload := &storage.GinRoot{Columns: main.Columns}
	if len(deferred.Columns) != 0 && len(deferred.Columns[0].Tokens) != 0 {
		payload.Pending = deferred.Columns
	}
	result := *built
	result.Gin = payload
	if outputs != nil {
		for i := range outputs.Outputs {
			if outputs.Outputs[i].Member {
				outputs.Outputs[i].GinPending = pending[next.Versions[i].Version]
			}
		}
	}
	return &result, ctx.Err()
}
