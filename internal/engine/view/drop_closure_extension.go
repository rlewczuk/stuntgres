package view

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// extensionRootOrder reports whether targets are extensions with their
// members, plus relations retired with them. PostgreSQL's findDependentObjects
// walks such a drop from each extension: its members are ordinary dependents,
// visited newest first and depth first, and the report is reverse post-order.
func extensionRootOrder(r DropReader, targets []ids.ObjectAddress) bool {
	if len(targets) == 0 || targets[0].ClassID != ids.ExtensionClassID {
		return false
	}
	extensions := make(map[ids.ObjectAddress]bool)
	for _, target := range targets {
		if target.ClassID == ids.ExtensionClassID {
			extensions[target] = true
		}
	}
	for _, target := range targets {
		if extensions[target] || extensions[catalog.ExtensionMembership(r.RetirementDescriptor(target))] {
			continue
		}
		if target.ClassID == ids.RelationClassID && target.SubID == 0 && r.Removed(target) {
			continue
		}
		return false
	}
	return true
}

// extensionRootExpansion is the root a walk in extension order expands when it
// reaches the unexpanded root address. Like PostgreSQL, reaching a member of
// another target extension first expands that extension; otherwise the
// reached root itself is expanded in place, not at its later root-loop turn.
func extensionRootExpansion(r DropReader, roots map[ids.ObjectAddress]bool, expanded func(ids.ObjectAddress) bool, address ids.ObjectAddress) (ids.ObjectAddress, bool) {
	if !roots[address] || expanded(address) {
		return ids.ObjectAddress{}, false
	}
	if owner := catalog.ExtensionMembership(r.RetirementDescriptor(address)); roots[owner] && !expanded(owner) {
		return owner, true
	}
	return address, true
}

// generatedArrayOwner is the element type that owns address, a generated
// array, through the INTERNAL dependency pg_type records for it.
func generatedArrayOwner(r DropReader, address ids.ObjectAddress) (ids.ObjectAddress, bool) {
	array, ok := r.RetirementDescriptor(address).(*catalog.Type)
	if !ok || address.SubID != 0 || !array.SQLType.IsArray() || array.SQLType.IsCatalogVector() {
		return ids.ObjectAddress{}, false
	}
	owner := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.SQLType.Element().ID())}
	element, ok := r.RetirementDescriptor(owner).(*catalog.Type)
	if !ok || element.Array != array.SQLType.ID() {
		return ids.ObjectAddress{}, false
	}
	return owner, true
}
