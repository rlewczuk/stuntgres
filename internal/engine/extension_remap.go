package engine

import (
	"slices"
	"sort"
	"strconv"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func remapExtensionDescriptor(d Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress) Descriptor {
	address := func(a ids.ObjectAddress) ids.ObjectAddress {
		if next, ok := mapping[a]; ok {
			return next
		}
		return a
	}
	switch d := d.(type) {
	case *typeDescriptor:
		next := address(d.extension)
		if next == d.extension {
			return d
		}
		n := *d
		n.extension = next
		return &n
	case *relationDescriptor:
		var addresses []ids.ObjectAddress
		for i, a := range d.AutoExtensions {
			next := address(a)
			if next != a && addresses == nil {
				addresses = append([]ids.ObjectAddress(nil), d.AutoExtensions...)
			}
			if addresses != nil {
				addresses[i] = next
			}
		}
		if addresses == nil {
			return d
		}
		sort.Slice(addresses, func(i, j int) bool { return addresses[i].ObjectID < addresses[j].ObjectID })
		n := *d
		n.AutoExtensions = addresses
		return &n
	case *extensionDescriptor:
		n := *d
		n.address = address(n.address)
		n.targetNamespace = address(n.targetNamespace)
		var members []ids.ObjectAddress
		for i, a := range d.members {
			mapped := address(a)
			if mapped != a && members == nil {
				members = slices.Clone(d.members)
			}
			if members != nil {
				members[i] = mapped
			}
		}
		if members != nil {
			n.members = members
		}
		if n.address == d.address && n.targetNamespace == d.targetNamespace && members == nil {
			return d
		}
		return &n
	case *procedureDescriptor:
		n := *d
		n.address = address(n.address)
		n.namespace = address(n.namespace)
		n.extension = address(n.extension)
		n.dictionary = address(n.dictionary)
		n.aggregate = remapOwnedAggregate(d.aggregate, n.address, n.namespace, mapping)
		if n.intarrayProvider() != nil {
			if n.name == "bqarr_in" {
				n.result = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n.result}).ObjectID
			} else {
				if oid, err := strconv.ParseUint(string(n.signature), 10, 32); err == nil {
					next := address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oid)})
					if next.ObjectID != ids.OID(oid) {
						n.signature = queryIntSignature(sqltype.ID(next.ObjectID))
					}
				}
			}
		}
		if n.address == d.address && n.namespace == d.namespace && n.extension == d.extension && n.dictionary == d.dictionary && n.signature == d.signature && n.result == d.result && n.aggregate == d.aggregate {
			return d
		}
		return &n
	case *tsTemplateDescriptor:
		n := *d
		n.address = address(n.address)
		n.namespace = address(n.namespace)
		n.extension = address(n.extension)
		n.init = address(n.init)
		n.lexize = address(n.lexize)
		return &n
	case *tsDictionaryDescriptor:
		n := *d
		n.address = address(n.address)
		n.namespace = address(n.namespace)
		n.extension = address(n.extension)
		n.template = address(n.template)
		return &n
	}
	return d
}
