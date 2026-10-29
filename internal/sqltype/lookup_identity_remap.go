package sqltype

// LookupIdentityRemapper remaps the scalar/domain/array identity graphs used by
// released native-provider declaration metadata. Changed nodes are lookup-only:
// this never mints executable codecs or transfers another driver's authority.
// The catalog's actual type import owner remains responsible for live codecs.
func LookupIdentityRemapper(identity func(ID) ID) func(*Descriptor) *Descriptor {
	memo := make(map[*Descriptor]*Descriptor)
	var remap func(*Descriptor) *Descriptor
	remap = func(d *Descriptor) *Descriptor {
		if d == nil {
			return nil
		}
		if n, ok := memo[d]; ok {
			return n
		}
		id, array := identity(d.id), identity(d.associatedArray)
		base, element := remap(d.base.Type), remap(d.element)
		if id == d.id && array == d.associatedArray && base == d.base.Type && element == d.element {
			memo[d] = d
			return d
		}
		n := *d
		n.id = id
		n.associatedArray = array
		n.base.Type = base
		n.element = element
		n.lookupOnly = true
		memo[d] = &n
		return &n
	}
	return remap
}
