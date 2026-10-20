package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type typeProvider struct{}

func (typeProvider) ClassID() ids.OID { return ids.TypeClassID }
func (typeProvider) Validate(view catalog.Read, descriptor catalog.Descriptor) error {
	t, ok := descriptor.(*catalog.Type)
	if !ok || t.SQLType == nil || t.SQLType.ID() == 0 {
		return fmt.Errorf("stuntgres: invalid type descriptor")
	}
	if t.ACLSyntheticDefault && !authorization.MatchesDefaultBaseline(t.ACL, t.Owner, authorization.DefaultTypes, false) {
		return fmt.Errorf("stuntgres: invalid synthetic type ACL")
	}
	ns, ok := view.Descriptor(descriptor.Namespace())
	if !ok || ns.Kind() != ids.NamespaceObject {
		return fmt.Errorf("stuntgres: type namespace is missing")
	}
	found := view.Lookup(catalog.DescriptorNameKey(descriptor))
	if len(found) != 1 || found[0] != descriptor.Address() {
		return fmt.Errorf("stuntgres: invalid type identity")
	}
	if t.Extension != (ids.ObjectAddress{}) {
		parent, ok := view.Extension(t.Extension)
		if !ok || parent.TargetNamespace != t.Namespace() {
			return fmt.Errorf("stuntgres: invalid installed type membership")
		}
		var err error
		switch {
		case parent.Name() == "intarray" && t.SQLType.IsQueryInt():
			err = stockIntarray.ValidateExtension(view, parent)
		case parent.Name() == "cube" && t.SQLType.IsCube():
			err = stockCube.ValidateExtension(view, parent)
		default:
			return fmt.Errorf("stuntgres: invalid installed type membership")
		}
		if err != nil {
			return err
		}
	} else if t.SQLType.IsQueryInt() || t.SQLType.IsCube() {
		return fmt.Errorf("stuntgres: installed scalar type has no installation")
	}
	if t.OwningRelation != (ids.ObjectAddress{}) {
		relation, ok := view.Relation(t.OwningRelation)
		if !ok || relation.Namespace() != t.Namespace() || relation.Owner != t.Owner {
			return fmt.Errorf("stuntgres: invalid row type ownership")
		}
		if !t.SQLType.IsArray() && (relation.RowType != t.SQLType.ID() || relation.Name() != t.Name()) {
			return catalog.Conflict{}
		}
		if t.SQLType.IsArray() && relation.RowType != t.SQLType.Element().ID() {
			return catalog.Conflict{}
		}
	}
	if t.SQLType.IsUserDefined() || t.SQLType.IsArray() && t.SQLType.Element().IsUserDefined() {
		if t.ObjectRevision == 0 || t.OwningRelation == (ids.ObjectAddress{}) && relationTypeNameOccupied(view, t.Namespace(), t.Name()) {
			return catalog.Conflict{}
		}
	}
	if t.SQLType.IsComposite() {
		relation, ok := view.Relation(t.OwningRelation)
		if !ok || len(relation.Columns) != t.SQLType.CompositeLen() {
			return fmt.Errorf("stuntgres: missing composite backing definition")
		}
		for i, column := range relation.Columns {
			member := t.SQLType.CompositeMember(i)
			actual, exists := view.LookupType(member.Usage.Type.ID())
			if !exists || actual != member.Usage.Type || member.Attribute != relation.Attributes[i] || member.Name != column.Name || member.Usage != column.Usage {
				return fmt.Errorf("stuntgres: inconsistent composite member generation")
			}
		}
	}
	if t.SQLType.IsDomain() {
		base := t.SQLType.DomainBase()
		if err := validateUsageCollation(view, base); err != nil {
			return err
		}
		actual, exists := view.LookupType(base.Type.ID())
		if !exists || actual != base.Type || t.SQLType.Concrete() && (!base.Valid() || t.ObjectRevision == 0) || t.Dimensions < 0 {
			return fmt.Errorf("stuntgres: invalid domain base generation")
		}
		if err := validateDomainCheckDefinitions(view, t); err != nil {
			return err
		}
	}
	if err := validateDomainDefault(view, t); err != nil {
		return err
	}
	if t.SQLType.IsArray() {
		element := t.SQLType.Element()
		actual, exists := view.LookupType(element.ID())
		if !exists || actual != element {
			return fmt.Errorf("stuntgres: invalid array element generation")
		}
		owner, exists := view.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(element.ID())})
		if !exists || owner.(*catalog.Type).Array != t.SQLType.ID() || owner.Namespace() != t.Namespace() || owner.(*catalog.Type).Owner != t.Owner {
			return fmt.Errorf("stuntgres: invalid associated array identity")
		}
	}
	if t.Array != 0 {
		array, exists := view.LookupType(t.Array)
		if !exists || !array.IsArray() || array.Element() != t.SQLType {
			return fmt.Errorf("stuntgres: invalid element array association")
		}
	}
	return nil
}

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
