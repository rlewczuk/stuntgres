package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func bindIndexProvider(parent *catalog.Relation, d *catalog.IndexDefinition) error {
	switch d.Method {
	case catalog.BtreeMethod:
		_, err := bindIndexOrdering(parent, d)
		return err
	case catalog.HashMethod:
		_, err := bindHashIndex(parent, d)
		return err
	case catalog.GistMethod:
		_, err := bindLogicalExclusionKeys(parent, d)
		return err
	default:
		return fmt.Errorf("stuntgres: invalid index method")
	}
}

type hashIndexBinding struct {
	slot       int
	expression expr.Expr
	derived    *catalog.DerivedIndexKey
	usage      sqltype.Usage
	class      sqltype.IndexClass
	provider   sqltype.IndexHash
}

func bindHashIndex(parent *catalog.Relation, d *catalog.IndexDefinition) (hashIndexBinding, error) {
	var b hashIndexBinding
	if d.Method != catalog.HashMethod || len(d.Keys) != 1 || len(d.Include) != 0 || d.Unique {
		return b, fmt.Errorf("stuntgres: invalid hash index definition")
	}
	key := d.Keys[0]
	if key.Desc || key.NullsFirst {
		return b, fmt.Errorf("stuntgres: ordered hash index key")
	}
	usage, err := indexKeyUsage(parent, key)
	if err != nil {
		return b, err
	}
	class := key.Class
	if class.Name == "" {
		var exists bool
		class, exists = d.Method.DefaultClass(usage.Type)
		if !exists {
			return b, indexDefaultClassError(usage.Type.DiagnosticName(), d.Method)
		}
	}
	b.provider, err = sqltype.BindIndexHash(usage, class)
	if err != nil {
		return b, err
	}
	b.usage, b.class = usage, class
	b.derived = key.Derived
	if key.Derived != nil {
		if key.Derived.Definition == nil || key.Derived.Execution == nil {
			return b, fmt.Errorf("stuntgres: missing hash expression")
		}
		b.slot, b.expression = -1, key.Derived.Execution
	} else {
		b.slot = slices.Index(parent.Attributes, key.Attribute)
		if b.slot < 0 {
			return b, fmt.Errorf("stuntgres: missing hash attribute")
		}
	}
	return b, nil
}

// Both AMs use the same typed/expression extraction authority.
func extractIndexKey(slot int, expression expr.Expr, values []sqltype.Value, evaluation *EvalContext) (sqltype.Value, error) {
	if slot >= 0 {
		return values[slot], nil
	}
	evaluation.Row = values
	return expression.Eval(evaluation)
}

type hashIndexEntry struct {
	key      sqltype.Value
	row      ids.RowID
	version  ids.VersionID
	position int
}
type hashIndexRoot struct {
	buckets    map[uint64][]hashIndexEntry
	rowEntries map[ids.RowID]*hashIndexEntry
	binding    hashIndexBinding
	storage    *relationStorage
	generation uint64
	prefix     int
	ends       *deletionRoot
}

func indexStorageIdentity(store *relationStorage) *relationStorage {
	if store.indexSource != nil {
		return store.indexSource
	}
	return store
}

func buildHashIndexRoot(evaluation *EvalContext, parent *catalog.Relation, d *catalog.IndexDefinition, store *relationStorage, materialize bool, capture *indexBuildCapture) (*logicalIndexRoot, error) {
	ctx := evaluation.Context
	binding, err := bindHashIndex(parent, d) // Eager even on empty/all-NULL input.
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("stuntgres: missing hash index storage")
	}
	var root *hashIndexRoot
	if materialize {
		root = &hashIndexRoot{buckets: make(map[uint64][]hashIndexEntry), binding: binding, storage: indexStorageIdentity(store), generation: store.generation, prefix: len(store.versions), ends: store.ends}
	}
	var preparation indexExecutionPreparation
	for position, row := range store.versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !liveVersion(row, ids.CommitSeq(^uint64(0)), store.ends, nil) {
			continue
		}
		matches, err := preparation.predicateMatches(evaluation, parent, d, row.values, nil, &binding)
		if err != nil {
			return nil, err
		}
		if capture != nil {
			capture.outputs[position] = indexRowOutput{index: capture.index, member: matches}
		}
		if !matches {
			continue
		}
		if err := preparation.prepareKeys(evaluation, parent, d, nil, &binding); err != nil {
			return nil, err
		}
		key, err := extractIndexKey(binding.slot, binding.expression, row.values, evaluation)
		if err != nil {
			return nil, err
		}
		if capture != nil {
			capture.outputs[position].key = key
		}
		if key.IsNull() {
			continue
		}
		h, err := binding.provider.Hash(ctx, key)
		if err != nil {
			return nil, err
		}
		if materialize {
			root.buckets[h] = append(root.buckets[h], hashIndexEntry{key: key, row: row.row, version: row.version, position: position})
		}
	}
	if !materialize {
		return nil, ctx.Err()
	}
	if err := root.captureRowEntries(ctx, d); err != nil {
		return nil, err
	}
	return &logicalIndexRoot{hash: root}, ctx.Err()
}

func publicationSlot(p *rowPublication, i int) int {
	if p.singleSlot >= 0 {
		return p.singleSlot
	}
	if p.slots != nil {
		return p.slots[i]
	}
	return len(p.root) + i
}

func mergeHashIndexRoot(evaluation *EvalContext, parent, index *catalog.Relation, old *logicalIndexRoot, p *rowPublication, reconstruct, materialize bool) (*logicalIndexRoot, error) {
	ctx := evaluation.Context
	d := index.SQLIndex
	if d.Method != catalog.HashMethod || len(d.Keys) != 1 || len(d.Include) != 0 || d.Unique {
		return nil, fmt.Errorf("stuntgres: invalid hash merge definition")
	}
	var binding hashIndexBinding
	var preparation indexExecutionPreparation
	if old == nil || old.hash == nil {
		if materialize {
			return nil, fmt.Errorf("stuntgres: missing hash root")
		}
		var err error
		binding, err = bindHashIndex(parent, d)
		if err != nil {
			return nil, err
		}
	} else {
		binding = old.hash.binding
		key := d.Keys[0]
		usage, err := indexKeyUsage(parent, key)
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
		if usage != binding.usage || class != binding.class || slot != binding.slot || key.Derived != binding.derived || key.Desc || key.NullsFirst {
			binding, err = bindHashIndex(parent, d)
			if err != nil {
				return nil, err
			}
		}
	}
	var ended map[ids.RowID]ids.VersionID
	var touched map[uint64]bool
	var added map[uint64][]hashIndexEntry
	touch := func(h uint64) {
		if touched == nil {
			touched = make(map[uint64]bool)
		}
		touched[h] = true
	}
	end := func(row rowVersion) error {
		if ended == nil {
			ended = make(map[ids.RowID]ids.VersionID)
		}
		ended[row.row] = row.version
		// A plain physical key can locate its bucket without evaluating SQL.
		// Expression keys are located below by their stored entry identity.
		if binding.expression == nil {
			key := row.values[binding.slot]
			if !key.IsNull() {
				h, err := binding.provider.Hash(ctx, key)
				if err != nil {
					return err
				}
				touch(h)
			}
		}
		return nil
	}
	for i, row := range p.writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The shared row owner validates predecessors in both phases. Ending
		// an index version consumes stored index identity, never old-row SQL.
		if row.base != 0 && materialize {
			position := publicationSlot(p, i)
			if position < 0 || position >= len(p.root) || p.root[position].row != row.row || p.root[position].version != row.base {
				return nil, fmt.Errorf("stuntgres: invalid hash update predecessor")
			}
			if err := end(p.root[position]); err != nil {
				return nil, err
			}
		}
		if p.deletion != nil {
			if deletion, ok := p.deletion.rows[row.row]; ok && deletion.version == row.version {
				continue
			}
		}
		output, produced := row.indexOutputs[index.Address()]
		produced = produced && output.matches(index)
		if !reconstruct && !produced && !rowMaintainsIndex(row, index) {
			continue
		}
		var key sqltype.Value
		if produced {
			if !output.member {
				continue
			}
			key = output.key
		} else {
			if d.HasRuntimeDomainConversion() && evaluation.liveScope() == nil && row.born == 0 {
				return nil, fmt.Errorf("stuntgres: missing produced hash index row")
			}
			matches, err := preparation.predicateMatches(evaluation, parent, d, row.values, nil, &binding)
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
			if err := preparation.prepareKeys(evaluation, parent, d, nil, &binding); err != nil {
				return nil, err
			}
			key, err = extractIndexKey(binding.slot, binding.expression, row.values, evaluation)
			if err != nil {
				return nil, err
			}
		}
		if key.IsNull() {
			continue
		}
		h, err := binding.provider.Hash(ctx, key)
		if err != nil {
			return nil, err
		}
		if materialize {
			touch(h)
			if added == nil {
				added = make(map[uint64][]hashIndexEntry)
			}
			added[h] = append(added[h], hashIndexEntry{key: key, row: row.row, version: row.version, position: publicationSlot(p, i)})
		}
	}
	if p.deletion != nil && materialize {
		for id, deletion := range p.deletion.rows {
			if deletion.own {
				continue
			}
			row, _, found := rowByID(p.root, id, deletion.ordinal)
			if !found || row.version != deletion.version {
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
	if len(ended) != 0 && binding.expression != nil {
		for h, bucket := range old.hash.buckets {
			if touched[h] {
				continue
			}
			for _, entry := range bucket {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if ended[entry.row] == entry.version {
					touch(h)
					break
				}
			}
		}
	}
	buckets := old.hash.buckets
	if len(touched) != 0 {
		buckets = make(map[uint64][]hashIndexEntry, len(old.hash.buckets)+len(added))
		for h, bucket := range old.hash.buckets {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			buckets[h] = bucket // Immutable untouched slices are shared.
		}
	}
	for h := range touched {
		bucket := old.hash.buckets[h]
		next := make([]hashIndexEntry, 0, len(bucket)+len(added[h]))
		for _, entry := range bucket {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if ended[entry.row] != entry.version {
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
	ends := p.ends
	if p.nextEnds != nil {
		ends = p.nextEnds
	}
	root := &hashIndexRoot{buckets: buckets, binding: binding, storage: p.store, generation: p.generation + 1, prefix: len(p.next), ends: ends}
	if err := root.captureRowEntries(ctx, d); err != nil {
		return nil, err
	}
	return &logicalIndexRoot{hash: root}, ctx.Err()
}

func (r *hashIndexRoot) captureRowEntries(ctx context.Context, d *catalog.IndexDefinition) error {
	if !d.IsExclusion() || !d.HasRuntimeDomainKeyConversion() {
		return nil
	}
	r.rowEntries = make(map[ids.RowID]*hashIndexEntry)
	for _, bucket := range r.buckets {
		for i := range bucket {
			if err := ctx.Err(); err != nil {
				return err
			}
			entry := &bucket[i]
			r.rowEntries[entry.row] = entry
		}
	}
	return nil
}
