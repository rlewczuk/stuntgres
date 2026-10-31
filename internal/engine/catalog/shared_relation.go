package catalog

import "stuntgres/internal/engine/ids"

// SharedRelation is the shared-catalog identity classification used by catalog
// metadata and database-owner maintenance. It never depends on a relation name.
func SharedRelation(d *Relation) bool {
	if d.RelationKind != CatalogRelation {
		return false
	}
	switch d.Address().ObjectID {
	case 1213, 1214, 1260, 1261, 1262, 2396, 2964, 3592, 6000, 6100, ids.ParameterClassID:
		return true
	}
	return false
}
