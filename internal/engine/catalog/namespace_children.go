package catalog

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/ids"
)

// Namespace retirement owns complete installations by their real target, not
// by the extension's database-wide name or generic Namespace field.
func NamespaceChildren(base Read, namespaces map[ids.ObjectAddress]bool) ([]ids.ObjectAddress, error) {
	selected := make(map[ids.ObjectAddress]bool)
	for address, descriptor := range base.Descriptors() {
		if namespaces[descriptor.Namespace()] {
			selected[address] = true
		}
		if namespace := OperatorFamilyNamespace(base, descriptor); namespace != (ids.ObjectAddress{}) && namespaces[namespace] {
			selected[address] = true
		}
		if ext, ok := descriptor.(*Extension); ok && namespaces[ext.TargetNamespace] {
			selected[address] = true
			for _, member := range ext.Members {
				d := base.Get(member)
				if d == nil || d.Namespace() != ext.TargetNamespace || ExtensionMembership(d) != address {
					return nil, fmt.Errorf("stuntgres: invalid namespace extension membership")
				}
				selected[member] = true
			}
		}
	}
	children := make([]ids.ObjectAddress, 0, len(selected))
	for address := range selected {
		children = append(children, address)
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].ClassID != children[j].ClassID {
			return children[i].ClassID < children[j].ClassID
		}
		return children[i].ObjectID < children[j].ObjectID
	})
	return children, nil
}
