package engine

import "sort"

func remapExtensionDescriptor(d Descriptor, mapping map[ObjectAddress]ObjectAddress) Descriptor {
	address := func(a ObjectAddress) ObjectAddress {
		if next, ok := mapping[a]; ok {
			return next
		}
		return a
	}
	switch d := d.(type) {
	case *relationDescriptor:
		var addresses []ObjectAddress
		for i, a := range d.autoExtensions {
			next := address(a)
			if next != a && addresses == nil {
				addresses = append([]ObjectAddress(nil), d.autoExtensions...)
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
		n.autoExtensions = addresses
		return &n
	case *extensionDescriptor:
		n := *d
		n.address = address(n.address)
		n.targetNamespace = address(n.targetNamespace)
		for i, a := range n.members {
			n.members[i] = address(a)
		}
		return &n
	case *procedureDescriptor:
		n := *d
		n.address = address(n.address)
		n.namespace = address(n.namespace)
		n.extension = address(n.extension)
		n.dictionary = address(n.dictionary)
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
