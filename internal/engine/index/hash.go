package index

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func bindIndexProvider(view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition) error {
	switch d.Method {
	case catalog.BtreeMethod:
		_, err := BindIndexOrdering(view, parent, d)
		return err
	case catalog.HashMethod:
		_, err := BindHashIndex(view, parent, d)
		return err
	case catalog.GistMethod:
		_, err := bindLogicalExclusionKeys(parent, d)
		return err
	default:
		return fmt.Errorf("stuntgres: invalid index method")
	}
}

func BindHashIndex(view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition) (storage.HashBinding, error) {
	var b storage.HashBinding
	if d.Method != catalog.HashMethod || len(d.Keys) != 1 || len(d.Include) != 0 || d.Unique {
		return b, fmt.Errorf("stuntgres: invalid hash index definition")
	}
	key := d.Keys[0]
	if key.Desc || key.NullsFirst {
		return b, fmt.Errorf("stuntgres: ordered hash index key")
	}
	usage, err := KeyUsage(parent, key)
	if err != nil {
		return b, err
	}
	class := key.Class
	if class.Name == "" {
		var exists bool
		class, exists = d.Method.DefaultClass(usage.Type)
		if !exists {
			return b, DefaultClassError(usage.Type.DiagnosticName(), d.Method)
		}
	}
	class, err = routine.BindClassSupport(view, class)
	if err != nil {
		return b, err
	}
	b.Provider, err = sqltype.BindIndexHash(usage, class)
	if err != nil {
		return b, err
	}
	b.Usage, b.Class = usage, class
	b.Derived = key.Derived
	if key.Derived != nil {
		if key.Derived.Definition == nil || key.Derived.Execution == nil {
			return b, fmt.Errorf("stuntgres: missing hash expression")
		}
		b.Slot, b.Expression = -1, key.Derived.Execution
	} else {
		b.Slot = slices.Index(parent.Attributes, key.Attribute)
		if b.Slot < 0 {
			return b, fmt.Errorf("stuntgres: missing hash attribute")
		}
	}
	return b, nil
}

// Both AMs use the same typed/expression extraction authority.
func extractIndexKey(slot int, expression expr.Expr, values []sqltype.Value, evaluation *exec.Context) (sqltype.Value, error) {
	if slot >= 0 {
		return values[slot], nil
	}
	evaluation.Row = values
	return expression.Eval(evaluation)
}

func BuildHashIndexRoot(evaluation *exec.Context, parent *catalog.Relation, d *catalog.IndexDefinition, store *storage.Relation, materialize bool, capture *BuildCapture) (*storage.IndexRoot, error) {
	ctx := evaluation.Context
	binding, err := BindHashIndex(evaluation.Catalog, parent, d) // Eager even on empty/all-NULL input.
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("stuntgres: missing hash index storage")
	}
	var root *storage.HashRoot
	if materialize {
		root = &storage.HashRoot{Buckets: make(map[uint64][]storage.HashEntry), Binding: binding, Storage: store.IndexIdentity(), Generation: store.Generation, Prefix: len(store.Versions), Ends: store.Ends}
	}
	var preparation ExecutionPreparation
	for position, row := range store.Versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), store.Ends, nil) {
			continue
		}
		matches, err := preparation.PredicateMatches(evaluation, parent, d, row.Values, nil, &binding)
		if err != nil {
			return nil, err
		}
		if capture != nil {
			capture.Outputs[position] = storage.IndexOutput{Index: capture.Index, Member: matches}
		}
		if !matches {
			continue
		}
		if err := preparation.PrepareKeys(evaluation, parent, d, nil, &binding); err != nil {
			return nil, err
		}
		key, err := extractIndexKey(binding.Slot, binding.Expression, row.Values, evaluation)
		if err != nil {
			return nil, err
		}
		if capture != nil {
			capture.Outputs[position].Key = key
		}
		if key.IsNull() {
			continue
		}
		h, err := binding.Provider.Hash(ctx, key)
		if err != nil {
			return nil, err
		}
		if materialize {
			root.Buckets[h] = append(root.Buckets[h], storage.HashEntry{Key: key, Row: row.Row, Version: row.Version, Position: position})
		}
	}
	if !materialize {
		return nil, ctx.Err()
	}
	if err := root.CaptureRowEntries(ctx, d); err != nil {
		return nil, err
	}
	return &storage.IndexRoot{Hash: root}, ctx.Err()
}

func PublicationSlot(p *RowPublication, i int) int {
	if p.SingleSlot >= 0 {
		return p.SingleSlot
	}
	if p.Slots != nil {
		return p.Slots[i]
	}
	return len(p.Root) + i
}

func MergeHashIndexRoot(evaluation *exec.Context, parent, index *catalog.Relation, old *storage.IndexRoot, p *RowPublication, reconstruct, materialize bool) (*storage.IndexRoot, error) {
	ctx := evaluation.Context
	d := index.SQLIndex
	if d.Method != catalog.HashMethod || len(d.Keys) != 1 || len(d.Include) != 0 || d.Unique {
		return nil, fmt.Errorf("stuntgres: invalid hash merge definition")
	}
	var binding storage.HashBinding
	var preparation ExecutionPreparation
	if old == nil || old.Hash == nil {
		if materialize {
			return nil, fmt.Errorf("stuntgres: missing hash root")
		}
		var err error
		binding, err = BindHashIndex(evaluation.Catalog, parent, d)
		if err != nil {
			return nil, err
		}
	} else {
		binding = old.Hash.Binding
		key := d.Keys[0]
		usage, err := KeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		class := key.Class
		if class.Name == "" {
			class, _ = d.Method.DefaultClass(usage.Type)
		}
		slot := slices.Index(parent.Attributes, key.Attribute)
		if key.Derived != nil {
			slot = -1
		}
		if usage != binding.Usage || class != binding.Class || slot != binding.Slot || key.Derived != binding.Derived || key.Desc || key.NullsFirst {
			binding, err = BindHashIndex(evaluation.Catalog, parent, d)
			if err != nil {
				return nil, err
			}
		}
	}
	var ended map[ids.RowID]ids.VersionID
	var touched map[uint64]bool
	var added map[uint64][]storage.HashEntry
	touch := func(h uint64) {
		if touched == nil {
			touched = make(map[uint64]bool)
		}
		touched[h] = true
	}
	end := func(row storage.RowVersion) error {
		if ended == nil {
			ended = make(map[ids.RowID]ids.VersionID)
		}
		ended[row.Row] = row.Version
		// A plain physical key can locate its bucket without evaluating SQL.
		// Expression keys are located below by their stored entry identity.
		if binding.Expression == nil {
			key := row.Values[binding.Slot]
			if !key.IsNull() {
				h, err := binding.Provider.Hash(ctx, key)
				if err != nil {
					return err
				}
				touch(h)
			}
		}
		return nil
	}
	for i, row := range p.Writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The shared row owner validates predecessors in both phases. Ending
		// an index version consumes stored index identity, never old-row SQL.
		if row.Base != 0 && materialize {
			position := PublicationSlot(p, i)
			if position < 0 || position >= len(p.Root) || p.Root[position].Row != row.Row || p.Root[position].Version != row.Base {
				return nil, fmt.Errorf("stuntgres: invalid hash update predecessor")
			}
			if err := end(p.Root[position]); err != nil {
				return nil, err
			}
		}
		if p.Deletion != nil {
			if deletion, ok := p.Deletion.Rows[row.Row]; ok && deletion.Version == row.Version {
				continue
			}
		}
		output, produced := row.IndexOutputs[index.Address()]
		produced = produced && OutputMatches(output, index)
		if !reconstruct && !produced && !RowMaintainsIndex(row, index) {
			continue
		}
		var key sqltype.Value
		if produced {
			if !output.Member {
				continue
			}
			key = output.Key
		} else {
			if d.HasRuntimeDomainConversion() && evaluation.LiveHost() == nil && row.Born == 0 {
				return nil, fmt.Errorf("stuntgres: missing produced hash index row")
			}
			matches, err := preparation.PredicateMatches(evaluation, parent, d, row.Values, nil, &binding)
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
			if err := preparation.PrepareKeys(evaluation, parent, d, nil, &binding); err != nil {
				return nil, err
			}
			key, err = extractIndexKey(binding.Slot, binding.Expression, row.Values, evaluation)
			if err != nil {
				return nil, err
			}
		}
		if key.IsNull() {
			continue
		}
		h, err := binding.Provider.Hash(ctx, key)
		if err != nil {
			return nil, err
		}
		if materialize {
			touch(h)
			if added == nil {
				added = make(map[uint64][]storage.HashEntry)
			}
			added[h] = append(added[h], storage.HashEntry{Key: key, Row: row.Row, Version: row.Version, Position: PublicationSlot(p, i)})
		}
	}
	if p.Deletion != nil && materialize {
		for id, deletion := range p.Deletion.Rows {
			if deletion.Own {
				continue
			}
			row, _, found := storage.RowByID(p.Root, id, deletion.Ordinal)
			if !found || row.Version != deletion.Version {
				return nil, fmt.Errorf("stuntgres: invalid hash deletion predecessor")
			}
			if err := end(row); err != nil {
				return nil, err
			}
		}
	}
	if !materialize {
		return nil, ctx.Err()
	}
	if len(ended) != 0 && binding.Expression != nil {
		for h, bucket := range old.Hash.Buckets {
			if touched[h] {
				continue
			}
			for _, entry := range bucket {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if ended[entry.Row] == entry.Version {
					touch(h)
					break
				}
			}
		}
	}
	buckets := old.Hash.Buckets
	if len(touched) != 0 {
		buckets = make(map[uint64][]storage.HashEntry, len(old.Hash.Buckets)+len(added))
		for h, bucket := range old.Hash.Buckets {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			buckets[h] = bucket // Immutable untouched slices are shared.
		}
	}
	for h := range touched {
		bucket := old.Hash.Buckets[h]
		next := make([]storage.HashEntry, 0, len(bucket)+len(added[h]))
		for _, entry := range bucket {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if ended[entry.Row] != entry.Version {
				next = append(next, entry)
			}
		}
		next = append(next, added[h]...)
		if len(next) == 0 {
			delete(buckets, h)
		} else {
			buckets[h] = next
		}
	}
	ends := p.Ends
	if p.NextEnds != nil {
		ends = p.NextEnds
	}
	root := &storage.HashRoot{Buckets: buckets, Binding: binding, Storage: p.Store, Generation: p.Generation + 1, Prefix: len(p.Next), Ends: ends}
	if err := root.CaptureRowEntries(ctx, d); err != nil {
		return nil, err
	}
	return &storage.IndexRoot{Hash: root}, ctx.Err()
}
