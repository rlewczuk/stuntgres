package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
