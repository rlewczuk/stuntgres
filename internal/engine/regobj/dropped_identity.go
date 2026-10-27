package regobj

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// DroppedAddress captures an object's identity while its descriptor and owners
// still exist. It shares IdentifyAddress's renderer and owner lookup, but keeps
// the dropped-object API's raw nullable names separate from inquiry spellings.
// Original and Normal belong to the deletion traversal and are left unset.
// The returned arrays are owned by the result and must not be mutated once the
// record is published. Missing objects return the zero value, with Found false.
func DroppedAddress(view catalog.Read, roles authorization.Read, path []ids.ObjectAddress, a ids.ObjectAddress) (catalog.DroppedObject, error) {
	var dropped catalog.DroppedObject
	identity, err := identifyAddress(view, roles, path, a, &dropped)
	if err != nil || !identity.Found {
		return catalog.DroppedObject{}, err
	}
	dropped.Address, dropped.Found = a, true
	dropped.Kind, dropped.Identity = identity.Kind, identity.Identity
	dropped.Names, dropped.Args = identity.Names, identity.Args
	if dropped.Names != nil && dropped.Args == nil {
		dropped.Args = []string{}
	}
	return dropped, nil
}

func droppedNamespace(view catalog.Read, namespace ids.ObjectAddress, dropped *catalog.DroppedObject) {
	if ns, ok := view.Namespace(namespace); ok {
		dropped.Schema, dropped.HasSchema = ns.Name(), true
		dropped.Temporary = ns.Temporary != (ids.SessionToken{})
		if dropped.Temporary {
			dropped.Schema = "pg_temp"
		}
	}
}
