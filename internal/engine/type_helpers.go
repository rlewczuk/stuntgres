package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func storedArrayElement(typ *sqltype.Descriptor) bool {
	if !typ.Concrete() {
		return false
	}
	physical := typ.BaseType()
	if physical.ID() == sqltype.Tid || sqltype.IsNetwork(physical.ID()) || sqltype.IsTransactionIdentifier(physical.ID()) {
		return false
	}
	if physical.IsArray() {
		return storedArrayElement(physical.Element())
	}
	if physical.IsComposite() {
		return physical.Concrete()
	}
	return sqltype.StoredScalarCastType(physical.ID()) || physical.ID() == sqltype.Regclass
}

func canonicalDeclarationTypeName(name string) string {
	switch name {
	case "smallint":
		name = "int2"
	case "integer":
		name = "int4"
	case "bigint":
		name = "int8"
	case "boolean":
		name = "bool"
	case "real":
		name = "float4"
	case "float", "double precision":
		name = "float8"
	case "decimal", "dec":
		name = "numeric"
	case "char", "character":
		name = "bpchar"
	case "character varying":
		name = "varchar"
	case "bit varying":
		name = "varbit"
	}
	return name
}

func typeDiagnostic(raw parser.TypeName, code, message string) error {
	if raw.Pos.Character > 0 {
		return insertError(code, message, raw.Pos)
	}
	return sqlError(code, message)
}

// Recognized PostgreSQL types outside the concrete storage authority are a
// capability boundary, not nonexistent names. Classification belongs to binding
// so IF NOT EXISTS can skip it, never to the raw syntax parser.
func unsupportedColumnType(raw parser.TypeName) bool {
	switch raw.Name {
	case "smallserial", "serial2", "serial", "serial4", "bigserial", "serial8":
		// CREATE recognizes these utility pseudo names even when quoted.
		return true
	case "char",
		"timetz",
		"timestamptz", "xml",
		"name", "macaddr", "macaddr8", "point", "line",
		"lseg", "box", "path", "polygon", "circle", "record":
		return true
	}
	if !raw.Quoted {
		switch raw.Name {
		case "double", "precision":
			return true
		}
	}
	return false
}

// Existing row-bearing relations reserve their reciprocal type identity.
func relationTypeNameOccupied(view catalog.Read, namespace ids.ObjectAddress, name string) bool {
	for _, address := range view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name}) {
		if relation, ok := view.Relation(address); ok && (relation.RelationKind == catalog.TableRelation || relation.RelationKind == catalog.PartitionedRelation || relation.RelationKind == catalog.ViewRelation || relation.RelationKind == catalog.CompositeRelation) {
			return true
		}
	}
	return false
}
