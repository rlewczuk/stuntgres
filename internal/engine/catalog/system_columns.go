package catalog

// IsSystemColumn reports whether name is one of the system column names
// every table reserves.
func IsSystemColumn(name string) bool {
	switch name {
	case "ctid", "xmin", "cmin", "xmax", "cmax", "tableoid":
		return true
	}
	return false
}

// HasSystemAttributes is the relation-kind authority shared by binding and
// catalog projections. CatalogView is stamped from the bootstrap view inventory.
func (d *Relation) HasSystemAttributes() bool {
	if d == nil {
		return false
	}
	switch d.RelationKind {
	case TableRelation, PartitionedRelation, MaterializedViewRelation, SequenceRelation, ForeignRelation:
		return true
	case CatalogRelation:
		return !d.CatalogView
	}
	return false
}

// HasSupportedAttribute admits live ordinary attributes and the supported
// executable system attribute, without changing dense-column consumers.
func (d *Relation) HasSupportedAttribute(sub int32) bool {
	if sub == -6 {
		return d.HasSystemAttributes()
	}
	return sub > 0 && d.HasAttribute(sub)
}
