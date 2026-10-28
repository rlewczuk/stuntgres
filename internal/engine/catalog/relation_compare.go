package catalog

// Immutable defining authority excludes compatible row/population publications.
// Shared by refresh and selected materialized ALTER admission; other statement
// admission does not acquire this compatibility fence.
func RelationDefiningAuthorityCompatible(old, current *Relation) bool {
	return current != nil && current.Address() == old.Address() && current.Namespace() == old.Namespace() &&
		current.Name() == old.Name() && current.Owner == old.Owner &&
		current.View == old.View && current.RowType == old.RowType && current.TableMethod == old.TableMethod &&
		len(current.Columns) == len(old.Columns) && len(current.Attributes) == len(old.Attributes) &&
		(len(old.Columns) == 0 || &current.Columns[0] == &old.Columns[0]) &&
		(len(old.Attributes) == 0 || &current.Attributes[0] == &old.Attributes[0])
}
