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
	Slot       int
	Expression expr.Expr
	Derived    *catalog.DerivedIndexKey
	Usage      sqltype.Usage
	Class      sqltype.IndexClass
	Provider   sqltype.IndexHash
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
func extractIndexKey(slot int, expression expr.Expr, values []sqltype.Value, evaluation *EvalContext) (sqltype.Value, error) {
	if slot >= 0 {
		return values[slot], nil
	}
	evaluation.Row = values
	return expression.Eval(evaluation)
}

type hashIndexEntry struct {
	Key      sqltype.Value
	Row      ids.RowID
	Version  ids.VersionID
	Position int
}
type hashIndexRoot struct {
	Buckets    map[uint64][]hashIndexEntry
	RowEntries map[ids.RowID]*hashIndexEntry
	Binding    hashIndexBinding
	Storage    *relationStorage
	Generation uint64
	Prefix     int
	Ends       *deletionRoot
}

// IndexIdentity is the storage an index root was built from: the origin for a
// detached index-build capture, otherwise the storage itself.
func (s *relationStorage) IndexIdentity() *relationStorage {
	if s.IndexSource != nil {
		return s.IndexSource
	}
	return s
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
		root = &hashIndexRoot{Buckets: make(map[uint64][]hashIndexEntry), Binding: binding, Storage: store.IndexIdentity(), Generation: store.Generation, Prefix: len(store.Versions), Ends: store.Ends}
	}
	var preparation indexExecutionPreparation
	for position, row := range store.Versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !liveVersion(row, ids.CommitSeq(^uint64(0)), store.Ends, nil) {
			continue
		}
		matches, err := preparation.predicateMatches(evaluation, parent, d, row.Values, nil, &binding)
		if err != nil {
			return nil, err
		}
		if capture != nil {
			capture.outputs[position] = indexRowOutput{Index: capture.index, Member: matches}
		}
		if !matches {
			continue
		}
		if err := preparation.prepareKeys(evaluation, parent, d, nil, &binding); err != nil {
			return nil, err
		}
		key, err := extractIndexKey(binding.Slot, binding.Expression, row.Values, evaluation)
		if err != nil {
			return nil, err
		}
		if capture != nil {
			capture.outputs[position].Key = key
		}
		if key.IsNull() {
			continue
		}
		h, err := binding.Provider.Hash(ctx, key)
		if err != nil {
			return nil, err
		}
		if materialize {
			root.Buckets[h] = append(root.Buckets[h], hashIndexEntry{Key: key, Row: row.Row, Version: row.Version, Position: position})
		}
	}
	if !materialize {
		return nil, ctx.Err()
	}
	if err := root.CaptureRowEntries(ctx, d); err != nil {
		return nil, err
	}
	return &logicalIndexRoot{Hash: root}, ctx.Err()
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
	if old == nil || old.Hash == nil {
		if materialize {
			return nil, fmt.Errorf("stuntgres: missing hash root")
		}
		var err error
		binding, err = bindHashIndex(parent, d)
		if err != nil {
			return nil, err
		}
	} else {
		binding = old.Hash.Binding
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
		if usage != binding.Usage || class != binding.Class || slot != binding.Slot || key.Derived != binding.Derived || key.Desc || key.NullsFirst {
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
	for i, row := range p.writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The shared row owner validates predecessors in both phases. Ending
		// an index version consumes stored index identity, never old-row SQL.
		if row.Base != 0 && materialize {
			position := publicationSlot(p, i)
			if position < 0 || position >= len(p.root) || p.root[position].Row != row.Row || p.root[position].Version != row.Base {
				return nil, fmt.Errorf("stuntgres: invalid hash update predecessor")
			}
			if err := end(p.root[position]); err != nil {
				return nil, err
			}
		}
		if p.deletion != nil {
			if deletion, ok := p.deletion.rows[row.Row]; ok && deletion.version == row.Version {
				continue
			}
		}
		output, produced := row.IndexOutputs[index.Address()]
		produced = produced && indexOutputMatches(output, index)
		if !reconstruct && !produced && !rowMaintainsIndex(row, index) {
			continue
		}
		var key sqltype.Value
		if produced {
			if !output.Member {
				continue
			}
			key = output.Key
		} else {
			if d.HasRuntimeDomainConversion() && evaluation.liveScope() == nil && row.Born == 0 {
				return nil, fmt.Errorf("stuntgres: missing produced hash index row")
			}
			matches, err := preparation.predicateMatches(evaluation, parent, d, row.Values, nil, &binding)
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
			if err := preparation.prepareKeys(evaluation, parent, d, nil, &binding); err != nil {
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
				added = make(map[uint64][]hashIndexEntry)
			}
			added[h] = append(added[h], hashIndexEntry{Key: key, Row: row.Row, Version: row.Version, Position: publicationSlot(p, i)})
		}
	}
	if p.deletion != nil && materialize {
		for id, deletion := range p.deletion.rows {
			if deletion.own {
				continue
			}
			row, _, found := rowByID(p.root, id, deletion.ordinal)
			if !found || row.Version != deletion.version {
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
		buckets = make(map[uint64][]hashIndexEntry, len(old.Hash.Buckets)+len(added))
		for h, bucket := range old.Hash.Buckets {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			buckets[h] = bucket // Immutable untouched slices are shared.
		}
	}
	for h := range touched {
		bucket := old.Hash.Buckets[h]
		next := make([]hashIndexEntry, 0, len(bucket)+len(added[h]))
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
	ends := p.ends
	if p.nextEnds != nil {
		ends = p.nextEnds
	}
	root := &hashIndexRoot{Buckets: buckets, Binding: binding, Storage: p.store, Generation: p.generation + 1, Prefix: len(p.next), Ends: ends}
	if err := root.CaptureRowEntries(ctx, d); err != nil {
		return nil, err
	}
	return &logicalIndexRoot{Hash: root}, ctx.Err()
}

func (r *hashIndexRoot) CaptureRowEntries(ctx context.Context, d *catalog.IndexDefinition) error {
	if !d.IsExclusion() || !d.HasRuntimeDomainKeyConversion() {
		return nil
	}
	r.RowEntries = make(map[ids.RowID]*hashIndexEntry)
	for _, bucket := range r.Buckets {
		for i := range bucket {
			if err := ctx.Err(); err != nil {
				return err
			}
			entry := &bucket[i]
			r.RowEntries[entry.Row] = entry
		}
	}
	return nil
}
