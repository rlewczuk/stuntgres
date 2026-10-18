package engine

import "stuntgres/internal/engine/catalog"

// A private version maintains only indexes ready when its statement accepted it.
// Nil is used by already committed/imported logical roots, not public write admission.
func rowMaintainsIndex(row rowVersion, index *catalog.Relation) bool {
	if row.indexAdmission == nil {
		return true
	}
	admitted, ok := row.indexAdmission.Relation(index.Address())
	return ok && admitted.SQLIndex != nil && admitted.SQLIndex.Ready && admitted.SQLIndex.Live
}
