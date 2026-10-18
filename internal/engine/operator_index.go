package engine

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// Membership only: descriptors, signatures and authorization always come from
// the same captured catalog root, never from this derived index.
type operatorNameKey struct {
	Namespace ids.ObjectAddress
	Name      string
}

type operatorNameIndex struct {
	Buckets map[operatorNameKey][]ids.ObjectAddress
}

var seededOperatorNames = func() *operatorNameIndex {
	index := &operatorNameIndex{Buckets: make(map[operatorNameKey][]ids.ObjectAddress)}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	for _, row := range builtinOperators {
		key := operatorNameKey{namespace, row.name}
		index.Buckets[key] = append(index.Buckets[key], ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: row.oid})
	}
	return index
}()

// One editor per unpublished root construction. The first membership change
// detaches the map; each touched bucket detaches at most once, even with spare
// capacity. Metadata-only replacements retain the entire immutable index.
type operatorIndexEdit struct {
	Index   *operatorNameIndex
	touched map[operatorNameKey]bool
}

func (edit *operatorIndexEdit) bucket(key operatorNameKey, extra int) []ids.ObjectAddress {
	if edit.touched == nil {
		size := 1
		if edit.Index != nil {
			size += len(edit.Index.Buckets)
		}
		buckets := make(map[operatorNameKey][]ids.ObjectAddress, size)
		if edit.Index != nil {
			for key, addresses := range edit.Index.Buckets {
				buckets[key] = addresses
			}
		}
		edit.Index = &operatorNameIndex{Buckets: buckets}
		edit.touched = make(map[operatorNameKey]bool)
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

func (edit *operatorIndexEdit) Replace(old, next Descriptor) {
	before, wasOperator := old.(*operatorDescriptor)
	after, isOperator := next.(*operatorDescriptor)
	if wasOperator && isOperator && before.Address() == after.Address() && before.Namespace() == after.Namespace() && before.Name() == after.Name() {
		return
	}
	if wasOperator {
		key := operatorNameKey{before.Namespace(), before.Name()}
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
		key := operatorNameKey{after.Namespace(), after.Name()}
		addresses := edit.bucket(key, 1)
		edit.Index.Buckets[key] = append(addresses, after.Address())
	}
}

// Wholesale identity reconstruction only; ordinary catalog changes use deltas.
// Call before any repair binder can capture the reconstructed descriptor maps.
func rebuildOperatorNames(descriptors iter.Seq2[ids.ObjectAddress, Descriptor]) *operatorNameIndex {
	index := &operatorNameIndex{Buckets: make(map[operatorNameKey][]ids.ObjectAddress)}
	for address, descriptor := range descriptors {
		if operator, ok := descriptor.(*operatorDescriptor); ok {
			key := operatorNameKey{operator.Namespace(), operator.Name()}
			index.Buckets[key] = append(index.Buckets[key], address)
		}
	}
	return index
}
