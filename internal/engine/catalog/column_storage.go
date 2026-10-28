package catalog

import "maps"

// ColumnStorage contains independent logical TOAST policy fields. Empty storage
// inherits the immutable type default; empty compression is the default method.
type ColumnStorage struct {
	Storage string
	Compression string
}

func (r *Relation) ColumnStorage(slot int) ColumnStorage {
	value := r.ColumnMeta.Storage[r.Attributes[slot]]
	if value.Storage == "" {
		value.Storage = r.Columns[slot].Usage.Type.AttributeFacts().Storage
	}
	return value
}

// WithColumnStorage never changes a published map or descriptor.
func (r *Relation) WithColumnStorage(slot int, value ColumnStorage) *Relation {
	if r.ColumnStorage(slot) == value {
		return r
	}
	if value.Storage == r.Columns[slot].Usage.Type.AttributeFacts().Storage {
		value.Storage = ""
	}
	next := *r
	next.ColumnMeta.Storage = maps.Clone(r.ColumnMeta.Storage)
	attribute := r.Attributes[slot]
	if value == (ColumnStorage{}) {
		delete(next.ColumnMeta.Storage, attribute)
	} else {
		if next.ColumnMeta.Storage == nil { next.ColumnMeta.Storage = make(map[int32]ColumnStorage) }
		next.ColumnMeta.Storage[attribute] = value
	}
	return &next
}
