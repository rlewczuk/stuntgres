package catalog

import "stuntgres/internal/sqltype"

// SystemAttribute resolves only executable system attributes. Reserved command
// IDs remain unavailable to expressions and all system attributes remain outside
// the dense ordinary-column schema.
func SystemAttribute(name string) (int32, sqltype.ID) {
	switch name {
	case "ctid":
		return -1, sqltype.Tid
	case "xmin":
		return -2, sqltype.Xid
	case "xmax":
		return -4, sqltype.Xid
	case "tableoid":
		return -6, sqltype.Oid
	}
	return 0, 0
}

func SystemAttributeName(sub int32) string {
	switch sub {
	case -1:
		return "ctid"
	case -2:
		return "xmin"
	case -4:
		return "xmax"
	case -6:
		return "tableoid"
	}
	return ""
}

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

// HasSupportedAttribute admits live ordinary and executable system attributes,
// without changing dense-column consumers.
func (d *Relation) HasSupportedAttribute(sub int32) bool {
	if SystemAttributeName(sub) != "" {
		return d.HasSystemAttributes()
	}
	return sub > 0 && d.HasAttribute(sub)
}
