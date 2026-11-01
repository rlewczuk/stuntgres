package index

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func bloomOption(name string) bool {
	if name == "length" {
		return true
	}
	if !strings.HasPrefix(name, "col") {
		return false
	}
	n, err := strconv.Atoi(name[3:])
	return err == nil && n >= 1 && n <= 32 && name == "col"+strconv.Itoa(n)
}

func BloomOptions(index *catalog.Relation) storage.BloomOptions {
	options := storage.BloomOptions{Words: 5}
	for i := range options.Bits {
		options.Bits[i] = 2
	}
	for _, value := range index.StorageOptions.Base {
		if value.Name == "length" {
			options.Words = (int(value.Integer) + 15) / 16
			continue
		}
		if bloomOption(value.Name) {
			n, _ := strconv.Atoi(value.Name[3:])
			options.Bits[n-1] = int(value.Integer)
		}
	}
	return options
}

func BindBloomIndex(view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition) ([]storage.BloomBinding, error) {
	if d.Method != catalog.BloomMethod || len(d.Keys) < 1 || len(d.Keys) > 32 || len(d.Include) != 0 || d.Unique || d.IsExclusion() {
		return nil, fmt.Errorf("stuntgres: invalid bloom definition")
	}
	program, err := bindLogicalKeys(view, parent, d)
	if err != nil {
		return nil, err
	}
	bindings := make([]storage.BloomBinding, len(d.Keys))
	for i, key := range program.Keys {
		usage, err := KeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		class := key.Class
		provider, err := sqltype.BindIndexHash(usage, class)
		if err != nil {
			return nil, err
		}
		bindings[i] = storage.BloomBinding{Usage: usage, Class: class, Provider: provider, Slot: program.Slots[i]}
	}
	return bindings, nil
}

// bloomRoot signs produced typed memberships. Existing immutable versions can
// share their signatures; destination controls pass no predecessor and rehash.
func bloomRoot(ctx context.Context, view catalog.Read, parent, index *catalog.Relation, entries []storage.IndexEntry, store *storage.Relation, generation uint64, prefix int, ends *storage.DeletionRoot, old *storage.IndexRoot) (*storage.IndexRoot, error) {
	bindings, err := BindBloomIndex(view, parent, index.SQLIndex)
	if err != nil {
		return nil, err
	}
	return bloomRootBound(ctx, index, entries, store, generation, prefix, ends, old, bindings)
}

func bloomRootBound(ctx context.Context, index *catalog.Relation, entries []storage.IndexEntry, store *storage.Relation, generation uint64, prefix int, ends *storage.DeletionRoot, old *storage.IndexRoot, bindings []storage.BloomBinding) (*storage.IndexRoot, error) {
	options := BloomOptions(index)
	root := &storage.BloomRoot{
		Bindings: bindings, Options: options, Entries: make([]storage.BloomEntry, len(entries)),
		Storage: store.IndexIdentity(), Generation: generation, Prefix: prefix, Ends: ends,
	}
	share := old != nil && old.Bloom != nil && old.Bloom.Options == options && len(old.Bloom.Bindings) == len(bindings)
	if share {
		for i, binding := range bindings {
			prior := old.Bloom.Bindings[i]
			if binding.Usage != prior.Usage || binding.Class != prior.Class || binding.Slot != prior.Slot {
				share = false
				break
			}
		}
	}
	// The shared membership merger and position transports preserve surviving
	// predecessor order and append new versions. Match that prefix linearly.
	count, prior := 0, 0
	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if share {
			for prior < len(old.Entries) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				candidate := old.Entries[prior]
				prior++
				if candidate.Row == entry.Row && candidate.Version == entry.Version {
					root.Entries[i].Signature = old.Bloom.Entries[prior-1].Signature
					break
				}
			}
		}
		if root.Entries[i].Signature == nil {
			count++
		}
	}
	words := make([]uint16, count*options.Words)
	at := 0
	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if root.Entries[i].Signature != nil {
			continue
		}
		mask := words[at : at+options.Words : at+options.Words]
		at += options.Words
		if len(entry.Keys) != len(bindings) {
			return nil, fmt.Errorf("stuntgres: invalid bloom membership")
		}
		for j, value := range entry.Keys {
			if err := storage.AddBloomValue(ctx, mask, options, j, &bindings[j].Provider, value); err != nil {
				return nil, err
			}
		}
		root.Entries[i].Signature = mask
	}
	var history any
	if old == nil {
		history = newConstruction(index.SQLIndex)
	} else {
		history = old.Construction
	}
	next := logicalIndexRoot(index.SQLIndex, entries, history)
	next.Bloom = root
	return next, ctx.Err()
}

func BuildBloomIndexRoot(evaluation *exec.Context, parent, index *catalog.Relation, store *storage.Relation, materialize bool, capture *BuildCapture) (*storage.IndexRoot, error) {
	// Eager binding rejects invalid providers even for no non-NULL values.
	bindings, err := BindBloomIndex(evaluation.Catalog, parent, index.SQLIndex)
	if err != nil {
		return nil, err
	}
	entries, err := BuildIndexEntries(evaluation, parent, index.SQLIndex, store, capture)
	if err != nil {
		return nil, err
	}
	if !materialize {
		return nil, evaluation.Context.Err()
	}
	return bloomRootBound(evaluation.Context, index, entries, store, store.Generation, len(store.Versions), store.Ends, nil, bindings)
}

func MergeBloomIndexRoot(evaluation *exec.Context, parent, index *catalog.Relation, old *storage.IndexRoot, p *RowPublication, reconstruct, materialize bool) (*storage.IndexRoot, error) {
	var entries []storage.IndexEntry
	if old != nil {
		entries = old.Entries
	}
	merged, err := MergeIndexRoot(evaluation, parent, index, entries, p, reconstruct, materialize, false, "", sqltype.OutputEnvironment{}, nil, nil)
	if err != nil || !materialize {
		return nil, err
	}
	if merged != nil {
		entries = merged.Entries
	}
	ends := p.Ends
	if p.NextEnds != nil {
		ends = p.NextEnds
	}
	return bloomRoot(evaluation.Context, evaluation.Catalog, parent, index, entries, p.Store, p.Generation+1, len(p.Next), ends, old)
}
