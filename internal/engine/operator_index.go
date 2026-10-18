package engine

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// Membership only: descriptors, signatures and authorization always come from
// the same captured catalog root, never from this derived index.
type operatorNameKey struct {
	namespace ids.ObjectAddress
	name      string
}

type operatorNameIndex struct {
	buckets map[operatorNameKey][]ids.ObjectAddress
}

var seededOperatorNames = func() *operatorNameIndex {
	index := &operatorNameIndex{buckets: make(map[operatorNameKey][]ids.ObjectAddress)}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	for _, row := range builtinOperators {
		key := operatorNameKey{namespace, row.name}
		index.buckets[key] = append(index.buckets[key], ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: row.oid})
	}
	return index
}()

// One editor per unpublished root construction. The first membership change
// detaches the map; each touched bucket detaches at most once, even with spare
// capacity. Metadata-only replacements retain the entire immutable index.
type operatorIndexEdit struct {
	index   *operatorNameIndex
	touched map[operatorNameKey]bool
}

func (edit *operatorIndexEdit) bucket(key operatorNameKey, extra int) []ids.ObjectAddress {
	if edit.touched == nil {
		size := 1
		if edit.index != nil {
			size += len(edit.index.buckets)
		}
		buckets := make(map[operatorNameKey][]ids.ObjectAddress, size)
		if edit.index != nil {
			for key, addresses := range edit.index.buckets {
				buckets[key] = addresses
			}
		}
		edit.index = &operatorNameIndex{buckets: buckets}
		edit.touched = make(map[operatorNameKey]bool)
	}
	addresses := edit.index.buckets[key]
	if !edit.touched[key] {
		detached := make([]ids.ObjectAddress, len(addresses), len(addresses)+extra)
		copy(detached, addresses)
		addresses = detached
		edit.touched[key] = true
	}
	return addresses
}

func (edit *operatorIndexEdit) replace(old, next Descriptor) {
	before, wasOperator := old.(*operatorDescriptor)
	after, isOperator := next.(*operatorDescriptor)
	if wasOperator && isOperator && before.address == after.address && before.namespace == after.namespace && before.name == after.name {
		return
	}
	if wasOperator {
		key := operatorNameKey{before.namespace, before.name}
		addresses := edit.bucket(key, 0)
		for i, address := range addresses {
			if address == before.address {
				copy(addresses[i:], addresses[i+1:])
				addresses = addresses[:len(addresses)-1]
				break
			}
		}
		if len(addresses) == 0 {
			delete(edit.index.buckets, key)
		} else {
			edit.index.buckets[key] = addresses
		}
	}
	if isOperator {
		key := operatorNameKey{after.namespace, after.name}
		addresses := edit.bucket(key, 1)
		edit.index.buckets[key] = append(addresses, after.address)
	}
}

// Wholesale identity reconstruction only; ordinary catalog changes use deltas.
// Call before any repair binder can capture the reconstructed descriptor maps.
func rebuildOperatorNames(descriptors iter.Seq2[ids.ObjectAddress, Descriptor]) *operatorNameIndex {
	index := &operatorNameIndex{buckets: make(map[operatorNameKey][]ids.ObjectAddress)}
	for address, descriptor := range descriptors {
		if operator, ok := descriptor.(*operatorDescriptor); ok {
			key := operatorNameKey{operator.namespace, operator.name}
			index.buckets[key] = append(index.buckets[key], address)
		}
	}
	return index
}
