package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
)

// Presentation uses the same namespace-first identity lookup as binding.
func collationDisplayName(view catalog.Read, id uint32, path []ids.ObjectAddress) string {
	if view.State() == nil {
		return pgIdentifier(typedef.CollationName(view, id))
	}
	d, ok := view.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)})
	if !ok {
		return ""
	}
	if path == nil && d.Seeded() {
		return pgIdentifier(d.Name())
	}
	for _, ns := range path {
		if a, ok := view.LookupName(ids.NameKey{ClassID: ids.CollationClassID, Namespace: ns, Name: d.Name()}); ok {
			if a == d.Address() {
				return pgIdentifier(d.Name())
			}
			break
		}
	}
	return pgIdentifier(view.Get(d.Namespace()).Name()) + "." + pgIdentifier(d.Name())
}

func (b *expressionBinder) collationName(id uint32) string {
	view := b.host.CurrentCatalog()
	if b.query.auth != nil {
		view = b.query.auth.ReadCatalog()
	}
	return typedef.CollationName(view, id)
}
