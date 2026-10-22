package catalog

import "stuntgres/internal/engine/ids"

// TypePinned is the pure catalog-owned bootstrap lifetime classification shared
// by ordinary drop pinning and family dependency construction. Lookup-only
// array codecs do not pin a user-defined element's lifetime.
func TypePinned(view Read, reader DefinitionReader, d *Type) bool {
	if d.OwningRelation != (ids.ObjectAddress{}) {
		descriptor, ok := DescriptorAt(view, reader, d.OwningRelation)
		relation, relationOK := descriptor.(*Relation)
		return !ok || !relationOK || relation.RelationKind == CatalogRelation
	}
	return !d.SQLType.IsUserDefined() && !(d.SQLType.IsArray() && (d.SQLType.Element().IsDomain() || d.SQLType.Element().IsUserDefined()))
}
