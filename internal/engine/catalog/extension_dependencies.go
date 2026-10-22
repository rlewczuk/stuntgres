package catalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
)

// ExtensionPrerequisites resolves the registered control's dependencies to
// actual installed identities. Catalog projection and dump ordering share it.
func ExtensionPrerequisites(v Read, ext *Extension) ([]ids.ObjectAddress, error) {
	implementation, ok := v.Owner().Extensions[ext.Name()]
	if !ok {
		return nil, fmt.Errorf("stuntgres: unregistered extension prerequisites")
	}
	requires := implementation.Control().Requires
	if len(requires) == 0 {
		return nil, nil
	}
	addresses := make([]ids.ObjectAddress, 0, len(requires))
	for _, name := range requires {
		a := v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name})
		if _, ok := v.Extension(a); !ok || a == ext.Address() {
			return nil, fmt.Errorf("stuntgres: missing installed extension prerequisite %q", name)
		}
		addresses = append(addresses, a)
	}
	return addresses, nil
}
