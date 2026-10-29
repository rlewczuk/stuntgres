package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
)

// Presentation uses the same namespace-first identity lookup as binding.
func CollationDisplayName(view catalog.Read, id uint32, path []ids.ObjectAddress) string {
	return collationDisplayName(view, id, path, catalog.Identifier)
}

// CollationName applies this deparser's path and identifier quoting policy.
func (r Deparser) CollationName(id uint32) string {
	return collationDisplayName(r.View, id, r.Path, r.Identifier)
}

func collationDisplayName(view catalog.Read, id uint32, path []ids.ObjectAddress, identifier func(string) string) string {
	if view.State() == nil {
		return identifier(typedef.CollationName(view, id))
	}
	d, ok := view.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)})
	if !ok {
		return ""
	}
	if path == nil && d.Seeded() {
		return identifier(d.Name())
	}
	for _, ns := range path {
		if a, ok := view.LookupName(ids.NameKey{ClassID: ids.CollationClassID, Namespace: ns, Name: d.Name()}); ok {
			if a == d.Address() {
				return identifier(d.Name())
			}
			break
		}
	}
	return identifier(view.Get(d.Namespace()).Name()) + "." + identifier(d.Name())
}
