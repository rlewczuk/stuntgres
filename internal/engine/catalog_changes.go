package engine

import (
	"sort"

	"stuntgres/internal/engine/ids"
)

func catalogStageChanges(s *CatalogStage) ChangeSet {
	changes := ChangeSet{Definitions: make([]ids.ObjectAddress, 0, len(s.descriptors)+len(s.removals))}
	for a := range s.descriptors {
		changes.Definitions = append(changes.Definitions, a)
	}
	for a := range s.removals {
		changes.Definitions = append(changes.Definitions, a)
	}
	sort.Slice(changes.Definitions, func(i, j int) bool {
		a, b := changes.Definitions[i], changes.Definitions[j]
		if a.ClassID != b.ClassID {
			return a.ClassID < b.ClassID
		}
		if a.ObjectID != b.ObjectID {
			return a.ObjectID < b.ObjectID
		}
		return a.SubID < b.SubID
	})
	for _, a := range changes.Definitions {
		if a.ClassID == ids.NamespaceClassID {
			changes.Namespaces = append(changes.Namespaces, a)
		}
	}
	return changes
}
