package catalog

import (
	"iter"
)

// SepgsqlClass deliberately excludes unsupported object classes and indexes.
func SepgsqlClass(d Descriptor, sub int32) string {
	if sub != 0 {
		if r, ok := d.(*Relation); ok && (r.RelationKind == TableRelation || r.RelationKind == PartitionedRelation || r.RelationKind == CatalogRelation && !r.CatalogView) && SepgsqlAttributeName(r, sub) != "" {
			return "db_column"
		}
		return ""
	}
	switch d := d.(type) {
	case *Database:
		return "db_database"
	case *Namespace:
		return "db_schema"
	case *Procedure:
		return "db_procedure"
	case *Relation:
		if d.CatalogView {
			return "db_view"
		}
		switch d.RelationKind {
		case TableRelation, PartitionedRelation, CatalogRelation:
			return "db_table"
		case SequenceRelation:
			return "db_sequence"
		case ViewRelation:
			return "db_view"
		}
	}
	return ""
}

var sepgsqlSystemAttributes = [...]struct {
	name   string
	number int32
}{{"ctid", -1}, {"xmin", -2}, {"cmin", -3}, {"xmax", -4}, {"cmax", -5}, {"tableoid", -6}}

func SepgsqlAttributeName(r *Relation, sub int32) string {
	if sub < 0 && (r.RelationKind == TableRelation || r.RelationKind == PartitionedRelation || r.RelationKind == CatalogRelation) {
		for _, a := range sepgsqlSystemAttributes {
			if a.number == sub {
				return a.name
			}
		}
	}
	if i := r.IndexForAttribute(sub); i >= 0 {
		return r.Columns[i].Name
	}
	return ""
}
func SepgsqlAttributes(r *Relation) iter.Seq[int32] {
	return func(yield func(int32) bool) {
		for _, sub := range r.Attributes {
			if !yield(sub) {
				return
			}
		}
		if r.RelationKind == TableRelation || r.RelationKind == PartitionedRelation || r.RelationKind == CatalogRelation && !r.CatalogView {
			for _, a := range sepgsqlSystemAttributes {
				if !yield(a.number) {
					return
				}
			}
		}
	}
}

// SepgsqlObjectKey follows quote_object_name, quoting each component rather
// than treating literal dots as structural separators.
func SepgsqlObjectKey(view Read, d Descriptor, sub int32) string {
	quote := DependencyIdentifier
	database := view.MustDatabase(view.Owner().DatabaseAddress()).Name()
	if db, ok := d.(*Database); ok {
		return quote(db.Name())
	}
	key := quote(database)
	if ns, ok := d.(*Namespace); ok {
		return key + "." + quote(ns.Name())
	}
	if ns, ok := view.Namespace(d.Namespace()); ok {
		key += "." + quote(ns.Name())
	}
	key += "." + quote(d.Name())
	if sub != 0 {
		if r, ok := d.(*Relation); ok {
			key += "." + quote(SepgsqlAttributeName(r, sub))
		}
	}
	return key
}
