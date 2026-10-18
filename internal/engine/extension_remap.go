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
		next := address(d.Extension)
		if next == d.Extension {
			return d
		}
		n := *d
		n.Extension = next
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
		n.ObjectAddress = address(n.Address())
		n.TargetNamespace = address(n.TargetNamespace)
		var members []ids.ObjectAddress
		for i, a := range d.Members {
			mapped := address(a)
			if mapped != a && members == nil {
				members = slices.Clone(d.Members)
			}
			if members != nil {
				members[i] = mapped
			}
		}
		if members != nil {
			n.Members = members
		}
		if n.Address() == d.Address() && n.TargetNamespace == d.TargetNamespace && members == nil {
			return d
		}
		return &n
	case *procedureDescriptor:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.ObjectNamespace = address(n.Namespace())
		n.Extension = address(n.Extension)
		n.Dictionary = address(n.Dictionary)
		n.Aggregate = remapOwnedAggregate(d.Aggregate, n.Address(), n.Namespace(), mapping)
		if intarrayProviderOf(&n) != nil {
			if n.Name() == "bqarr_in" {
				n.Result = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n.Result}).ObjectID
			} else {
				if oid, err := strconv.ParseUint(string(n.Signature), 10, 32); err == nil {
					next := address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oid)})
					if next.ObjectID != ids.OID(oid) {
						n.Signature = queryIntSignature(sqltype.ID(next.ObjectID))
					}
				}
			}
		}
		if n.Address() == d.Address() && n.Namespace() == d.Namespace() && n.Extension == d.Extension && n.Dictionary == d.Dictionary && n.Signature == d.Signature && n.Result == d.Result && n.Aggregate == d.Aggregate {
			return d
		}
		return &n
	case *tsTemplateDescriptor:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.ObjectNamespace = address(n.Namespace())
		n.Extension = address(n.Extension)
		n.Init = address(n.Init)
		n.Lexize = address(n.Lexize)
		return &n
	case *tsDictionaryDescriptor:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.ObjectNamespace = address(n.Namespace())
		n.Extension = address(n.Extension)
		n.Template = address(n.Template)
		return &n
	}
	return d
}
