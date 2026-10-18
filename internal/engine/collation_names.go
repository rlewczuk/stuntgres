package engine

import "stuntgres/internal/engine/ids"

// Presentation uses the same namespace-first identity lookup as binding.
func collationDisplayName(view CatalogRead, id uint32, path []ids.ObjectAddress) string {
	if view.state == nil {
		return pgIdentifier(collationName(view, id))
	}
	d, ok := view.state.descriptors[ids.ObjectAddress{ClassID: CollationClassID, ObjectID: ids.OID(id)}].(*collationDescriptor)
	if !ok {
		return ""
	}
	if path == nil && d.seeded() {
		return pgIdentifier(d.name)
	}
	for _, ns := range path {
		if a, ok := view.state.names[ids.NameKey{ClassID: CollationClassID, Namespace: ns, Name: d.name}]; ok {
			if a == d.Address() {
				return pgIdentifier(d.name)
			}
			break
		}
	}
	return pgIdentifier(view.state.descriptors[d.Namespace()].Name()) + "." + pgIdentifier(d.name)
}

func (b *expressionBinder) collationName(id uint32) string {
	view := CatalogRead{owner: b.engine, state: b.engine.state}
	if b.queryAuth != nil {
		view = b.queryAuth.catalog
	}
	return collationName(view, id)
}
