package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// Membership only: descriptors, signatures and authorization always come from
// the same captured catalog root, never from this derived index.
type OperatorNameKey struct {
	Namespace ids.ObjectAddress
	Name      string
}

type OperatorNameIndex struct {
	Buckets map[OperatorNameKey][]ids.ObjectAddress
}

// One editor per unpublished root construction. The first membership change
// detaches the map; each touched bucket detaches at most once, even with spare
// capacity. Metadata-only replacements retain the entire immutable index.
type OperatorIndexEdit struct {
	Index   *OperatorNameIndex
	touched map[OperatorNameKey]bool
}

func (edit *OperatorIndexEdit) bucket(key OperatorNameKey, extra int) []ids.ObjectAddress {
	if edit.touched == nil {
		var buckets map[OperatorNameKey][]ids.ObjectAddress
		if edit.Index != nil {
			buckets = edit.Index.Buckets
		}
		edit.Index = &OperatorNameIndex{Buckets: cloneMap(buckets)}
		edit.touched = make(map[OperatorNameKey]bool)
	}
	addresses := edit.Index.Buckets[key]
	if !edit.touched[key] {
		detached := make([]ids.ObjectAddress, len(addresses), len(addresses)+extra)
		copy(detached, addresses)
		addresses = detached
		edit.touched[key] = true
	}
	return addresses
}

func (edit *OperatorIndexEdit) Replace(old, next Descriptor) {
	before, wasOperator := old.(*Operator)
	after, isOperator := next.(*Operator)
	if wasOperator && isOperator && before.Address() == after.Address() && before.Namespace() == after.Namespace() && before.Name() == after.Name() {
		return
	}
	if wasOperator {
		key := OperatorNameKey{before.Namespace(), before.Name()}
		addresses := edit.bucket(key, 0)
		for i, address := range addresses {
			if address == before.Address() {
				copy(addresses[i:], addresses[i+1:])
				addresses = addresses[:len(addresses)-1]
				break
			}
		}
		if len(addresses) == 0 {
			delete(edit.Index.Buckets, key)
		} else {
			edit.Index.Buckets[key] = addresses
		}
	}
	if isOperator {
		key := OperatorNameKey{after.Namespace(), after.Name()}
		addresses := edit.bucket(key, 1)
		edit.Index.Buckets[key] = append(addresses, after.Address())
	}
}

// Wholesale identity reconstruction only; ordinary catalog changes use deltas.
// Call before any repair binder can capture the reconstructed descriptor maps.
func RebuildOperatorNames(descriptors iter.Seq2[ids.ObjectAddress, Descriptor]) *OperatorNameIndex {
	index := &OperatorNameIndex{Buckets: make(map[OperatorNameKey][]ids.ObjectAddress)}
	for address, descriptor := range descriptors {
		if operator, ok := descriptor.(*Operator); ok {
			key := OperatorNameKey{operator.Namespace(), operator.Name()}
			index.Buckets[key] = append(index.Buckets[key], address)
		}
	}
	return index
}
