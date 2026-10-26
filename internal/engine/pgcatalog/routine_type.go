package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Metadata accepts declared nominal types without requiring executable codecs.
func routineDataType(in Input, declared *catalog.Type, namespace *catalog.Namespace) (string, error) {
	typ := declared.SQLType
	if typ == nil {
		return "", fmt.Errorf("stuntgres: missing routine type descriptor")
	}
	if typ.IsArray() {
		element, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Element().ID())})
		if !ok || element.SQLType != typ.Element() || !typ.IsCatalogVector() && element.Array != typ.ID() {
			return "", fmt.Errorf("stuntgres: foreign routine type element")
		}
	}
	if typ.IsDomain() {
		base, ok := in.Catalog.LookupType(typ.DomainBase().Type.ID())
		if !ok || base != typ.DomainBase().Type {
			return "", fmt.Errorf("stuntgres: foreign routine type base")
		}
	}
	if typ.IsArray() && typ.AttributeFacts().Length == -1 {
		return "ARRAY", nil
	}
	if namespace.Name() == "pg_catalog" {
		return infoDomainsBaseName(in.Catalog, declared, in.TypeNamePath)
	}
	return "USER-DEFINED", nil
}
