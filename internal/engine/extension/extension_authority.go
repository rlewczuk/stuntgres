package extension

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// AuthorityObservation is what one extension script observed: the role
// revision and the namespace and database rows its stage was authorized
// against.
type AuthorityObservation struct {
	Roles               uint64
	Namespace, Database catalog.Descriptor
}

// Current reports whether a script's observation still holds against the
// published state and role revision; names are the journal's captured names.
// Extension scripts insert the database-wide row before their members.
// Preserve that public uniqueness diagnostic when a competing installation
// wins after this script's private snapshot; unrelated catalog fences retain
// the central publication-conflict contract. The name check does not depend
// on the observation, so repeating it per fence keeps the first error.
func (o AuthorityObservation) Current(names map[ids.NameKey]ids.ObjectAddress, state *catalog.State, roles uint64) error {
	for key, original := range names {
		if key.ClassID == ids.ExtensionClassID && original == (ids.ObjectAddress{}) && state.NameAddress(key) != original {
			return NameConflict(key.Name)
		}
	}
	if roles != o.Roles || o.Namespace == nil || o.Database == nil ||
		state.Get(o.Namespace.Address()) != o.Namespace ||
		state.Get(o.Database.Address()) != o.Database {
		return catalog.Conflict{}
	}
	return nil
}
