package engine

import "stuntgres/internal/engine/ids"

func materializedExtensionDepends(relation *relationDescriptor, target ids.ObjectAddress) bool {
	if target.ClassID != ExtensionClassID || target.SubID != 0 {
		return false
	}
	for _, address := range relation.autoExtensions {
		if address == target {
			return true
		}
	}
	return false
}
