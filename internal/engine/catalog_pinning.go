package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

// Pinning follows represented bootstrap authorities, never an OID cutoff.
func DependPinned(view catalog.Read, reader catalog.DefinitionReader, address ids.ObjectAddress) bool {
	if address.ClassID == ids.NamespaceClassID {
		return address.ObjectID == ids.PGCatalogNamespaceOID || address.ObjectID == ids.PGToastNamespaceOID
	}
	whole := address
	whole.SubID = 0
	d, ok := catalog.DescriptorAt(view, reader, whole)
	if !ok {
		return false
	}
	switch d := d.(type) {
	case *catalog.Type:
		if d.OwningRelation != (ids.ObjectAddress{}) {
			descriptor, ok := catalog.DescriptorAt(view, reader, d.OwningRelation)
			relation, relationOK := descriptor.(*catalog.Relation)
			return !ok || !relationOK || relation.RelationKind == catalog.CatalogRelation
		}
		// Lookup-only array codecs do not imply bootstrap lifetime. Automatic
		// arrays retain their owned element's dependency even without storage.
		return !d.SQLType.IsUserDefined() && !(d.SQLType.IsArray() && (d.SQLType.Element().IsDomain() || d.SQLType.Element().IsUserDefined()))
	case *catalog.Collation:
		return d.Seeded()
	case *catalog.Conversion:
		return d.Seeded()
	case *catalog.Procedure:
		return d.Seeded()
	case *catalog.Operator:
		return routine.OperatorSeeded(d)
	case *catalog.Relation:
		return d.RelationKind == catalog.CatalogRelation
	}
	return false
}
