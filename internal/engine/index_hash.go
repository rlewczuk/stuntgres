package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/sqltype"
)

type indexMethod uint8

const (
	btreeMethod indexMethod = iota
	hashMethod
)

func (m indexMethod) String() string {
	if m == hashMethod {
		return "hash"
	}
	return "btree"
}
func (m indexMethod) defaultClass(t *sqltype.Descriptor) (sqltype.IndexClass, bool) {
	if m == hashMethod {
		return sqltype.DefaultHashIndexClass(t)
	}
	return sqltype.DefaultIndexClass(t)
}
func (m indexMethod) defaultClassID(id sqltype.ID) (sqltype.IndexClass, bool) {
	if m == hashMethod {
		return sqltype.DefaultHashIndexClassID(id)
	}
	return sqltype.DefaultIndexClassID(id)
}
func (m indexMethod) lookupClass(name string) (sqltype.IndexClass, bool) {
	if m == hashMethod {
		return sqltype.LookupHashIndexClass(name)
	}
	return sqltype.LookupIndexClass(name)
}

func bindIndexProvider(parent *relationDescriptor, d *indexDefinition) error {
	switch d.method {
	case btreeMethod:
		_, err := bindIndexOrdering(parent, d)
		return err
	case hashMethod:
		_, err := bindHashIndex(parent, d)
		return err
	default:
		return fmt.Errorf("stuntgres: invalid index method")
	}
}

type hashIndexBinding struct {
	slot       int
	expression BoundExpr
	derived    *derivedIndexKey
	usage      sqltype.Usage
	class      sqltype.IndexClass
	provider   sqltype.IndexHash
}

func bindHashIndex(parent *relationDescriptor, d *indexDefinition) (hashIndexBinding, error) {
	var b hashIndexBinding
	if d.method != hashMethod || len(d.keys) != 1 || len(d.include) != 0 || d.unique {
		return b, fmt.Errorf("stuntgres: invalid hash index definition")
	}
	key := d.keys[0]
	if key.desc || key.nullsFirst {
		return b, fmt.Errorf("stuntgres: ordered hash index key")
	}
	usage, err := indexKeyUsage(parent, key)
	if err != nil {
		return b, err
	}
	class := key.class
	if class.Name == "" {
		var exists bool
		class, exists = d.method.defaultClass(usage.Type)
		if !exists {
			return b, indexDefaultClassError(usage.Type.DiagnosticName(), d.method)
		}
	}
	b.provider, err = sqltype.BindIndexHash(usage, class)
	if err != nil {
		return b, err
	}
	b.usage, b.class = usage, class
	b.derived = key.derived
	if key.derived != nil {
		if key.derived.definition == nil || key.derived.execution == nil {
			return b, fmt.Errorf("stuntgres: missing hash expression")
		}
		b.slot, b.expression = -1, key.derived.execution
	} else {
		b.slot = slices.Index(parent.attributes, key.attribute)
		if b.slot < 0 {
			return b, fmt.Errorf("stuntgres: missing hash attribute")
		}
	}
	return b, nil
}

// Both AMs use the same typed/expression extraction authority.
func extractIndexKey(slot int, expression BoundExpr, values []sqltype.Value, evaluation *EvalContext) (sqltype.Value, error) {
	if slot >= 0 {
		return values[slot], nil
	}
	evaluation.Row = values
	return expression.Eval(evaluation)
}

type hashIndexEntry struct {
	key      sqltype.Value
	row      RowID
	version  VersionID
	position int
}
type hashIndexRoot struct {
	buckets    map[uint64][]hashIndexEntry
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

func buildHashIndexRoot(ctx context.Context, parent *relationDescriptor, d *indexDefinition, store *relationStorage, materialize bool) (*logicalIndexRoot, error) {
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
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1}
	for position, row := range store.versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !liveVersion(row, CommitSeq(^uint64(0)), store.ends, nil) {
			continue
		}
		matches, err := indexPredicateMatches(&evaluation, d.predicate, row.values)
		if err != nil {
			return nil, err
		}
		if !matches {
			continue
		}
		key, err := extractIndexKey(binding.slot, binding.expression, row.values, &evaluation)
		if err != nil {
			return nil, err
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

func mergeHashIndexRoot(ctx context.Context, parent, index *relationDescriptor, old *logicalIndexRoot, p *rowPublication, reconstruct, materialize bool) (*logicalIndexRoot, error) {
	d := index.sqlIndex
	if d.method != hashMethod || len(d.keys) != 1 || len(d.include) != 0 || d.unique {
		return nil, fmt.Errorf("stuntgres: invalid hash merge definition")
	}
	var binding hashIndexBinding
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
		key := d.keys[0]
		usage, err := indexKeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		class := key.class
		if class.Name == "" {
			class, _ = d.method.defaultClass(usage.Type)
		}
		slot := slices.Index(parent.attributes, key.attribute)
		if key.derived != nil {
			slot = -1
		}
		if usage != binding.usage || class != binding.class || slot != binding.slot || key.derived != binding.derived || key.desc || key.nullsFirst {
			binding, err = bindHashIndex(parent, d)
			if err != nil {
				return nil, err
			}
		}
	}
	var ended map[RowID]VersionID
	var touched map[uint64]bool
	var added map[uint64][]hashIndexEntry
	touch := func(h uint64) {
		if touched == nil {
			touched = make(map[uint64]bool)
		}
		touched[h] = true
	}
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1}
	end := func(row rowVersion) error {
		matches, err := indexPredicateMatches(&evaluation, d.predicate, row.values)
		if err != nil || !matches {
			return err
		}
		key, err := extractIndexKey(binding.slot, binding.expression, row.values, &evaluation)
		if err != nil || key.IsNull() {
			return err
		}
		h, err := binding.provider.Hash(ctx, key)
		if err == nil && materialize {
			if ended == nil {
				ended = make(map[RowID]VersionID)
			}
			ended[row.row] = row.version
			touch(h)
		}
		return err
	}
	for i, row := range p.writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The shared row owner validates predecessors in both phases. Only
		// materialization needs their old hashes to remove bucket membership.
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
		if !reconstruct && !rowMaintainsIndex(row, index) {
			continue
		}
		matches, err := indexPredicateMatches(&evaluation, d.predicate, row.values)
		if err != nil {
			return nil, err
		}
		if !matches {
			continue
		}
		key, err := extractIndexKey(binding.slot, binding.expression, row.values, &evaluation)
		if err != nil {
			return nil, err
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
	return &logicalIndexRoot{hash: root}, ctx.Err()
}
