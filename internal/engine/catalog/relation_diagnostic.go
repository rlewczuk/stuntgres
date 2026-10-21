package catalog

// RelationDiagnosticKind names the kind of r the way PostgreSQL diagnostics
// do ("table", "view", "sequence", ...).
func RelationDiagnosticKind(r *Relation) string {
	if r.RelationKind == ForeignRelation {
		return "foreign table"
	}
	if r.RelationKind == CompositeRelation {
		return "composite type"
	}
	if r.RelationKind == MaterializedViewRelation {
		return "materialized view"
	}
	if r.RelationKind == IndexRelation {
		return "index"
	}
	if r.RelationKind == ViewRelation {
		return "view"
	}
	if r.RelationKind == SequenceRelation {
		return "sequence"
	}
	return "table"
}
