package engine

import (
	"context"
	"slices"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// uniqueKeyValues is a claim's comparer: index ordering decides equality.
type uniqueKeyValues struct {
	Ordering indexOrdering
	Values   []sqltype.Value
}

func (k *uniqueKeyValues) EqualKey(ctx context.Context, stored any) (bool, error) {
	cmp, err := k.Ordering.CompareKeys(ctx, storage.IndexEntry{Keys: stored.([]sqltype.Value)}, storage.IndexEntry{Keys: k.Values})
	return err == nil && cmp == 0, err
}

func (k *uniqueKeyValues) OwnedKey() any { return slices.Clone(k.Values) }

// Both lock interning and the conflict command's private overlay use this
// provider bucket. Ordering comparison, never the hash, decides equality.
func UniqueKeyHash(ctx context.Context, ordering indexOrdering, grouping []sqltype.Grouping, values []sqltype.Value) (uint64, error) {
	hash := uint64(14695981039346656037)
	for i, value := range values {
		var h uint64
		class := ordering.Keys[i].Class
		defaultClass, _ := sqltype.DefaultIndexClass(value.Type())
		// Nondefault equality can be coarser than the type's grouping hash
		// (TEXT bpchar_ops ignores trailing spaces). Compare within one bucket.
		if class.Family != "network_ops" && (class.Name == "" || class.Name == defaultClass.Name) {
			var err error
			h, err = grouping[i].Hash(ctx, value)
			if err != nil {
				return 0, err
			}
		}
		hash = (hash ^ h) * 1099511628211
	}
	return hash, nil
}
