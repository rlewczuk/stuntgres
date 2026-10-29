package typedef

import (
	"fmt"
	"unicode/utf8"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// AssociatedArrayNameCandidate is the single identifier construction rule used
// both by the SQL owner and by logical replay's finite naming-trace solver.
func AssociatedArrayNameCandidate(name string, suffix uint64) string {
	tail := ""
	if suffix != 0 {
		tail = fmt.Sprintf("_%d", suffix)
	}
	prefix := name
	for len(prefix) > 62-len(tail) {
		_, width := utf8.DecodeLastRuneInString(prefix)
		prefix = prefix[:len(prefix)-width]
	}
	return "_" + prefix + tail
}

func ChooseAssociatedArrayName(name string, occupied func(string) bool) string {
	arrayName := AssociatedArrayNameCandidate(name, 0)
	for suffix := uint64(1); occupied(arrayName); suffix++ {
		arrayName = AssociatedArrayNameCandidate(name, suffix)
	}
	return arrayName
}

// The executable projection shares one authority with named and supported user
// rows; catalog/builtin row identities remain lookup-only.
func CompositeMembers(relation *catalog.Relation) []sqltype.CompositeMember {
	members := make([]sqltype.CompositeMember, len(relation.Columns))
	for i, c := range relation.Columns {
		members[i] = sqltype.CompositeMember{Attribute: relation.Attributes[i], Name: c.Name, Usage: c.Usage}
	}
	return members
}

// SynchronizeRowTypeMetadata is the shared metadata-only cooking core. replace
// records each actual effect before the next lookup, preserving row/array name
// occupancy and provider order without constructing a catalog or stage.
func SynchronizeRowTypeMetadata(relation *catalog.Relation, row *catalog.Type, descriptor func(ids.ObjectAddress) catalog.Descriptor, associatedName func(ids.ObjectAddress, string) string, replace func(catalog.Descriptor) error) error {
	same := row.Namespace() == relation.Namespace() && row.Name() == relation.Name()
	if row.SQLType.IsComposite() {
		same = same && row.SQLType.CompositeLen() == len(relation.Columns)
		if same {
			for i, column := range relation.Columns {
				member := row.SQLType.CompositeMember(i)
				if member.Attribute != relation.Attributes[i] || member.Name != column.Name || member.Usage != column.Usage {
					same = false
					break
				}
			}
		}
	}
	if same && row.Owner == relation.Owner {
		return nil
	}
	next := *row
	if row.Owner != relation.Owner {
		next.ACL = authorization.SchemaACLOwner(row.ACL, row.Owner, relation.Owner)
	}
	next.ObjectNamespace, next.Owner, next.ObjectRevision = relation.Namespace(), relation.Owner, relation.Revision()
	if same {
		// Ownership is metadata, not a new codec or composite signature.
		next.SQLType = row.SQLType
	} else if row.SQLType.IsComposite() {
		ns := descriptor(relation.Namespace())
		var err error
		next.SQLType, err = sqltype.Composite(row.SQLType.ID(), relation.Name(), catalog.DependencyIdentifier(ns.Name())+"."+catalog.DependencyIdentifier(relation.Name()), row.Array, CompositeMembers(relation))
		if err != nil {
			return err
		}
	} else {
		next.SQLType = sqltype.LookupOnly(row.SQLType.ID(), relation.Name(), row.Array)
	}
	if err := replace(&next); err != nil {
		return err
	}
	if row.Array != 0 {
		array := descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}).(*catalog.Type)
		copy := *array
		if array.Owner != relation.Owner {
			copy.ACL = authorization.SchemaACLOwner(array.ACL, array.Owner, relation.Owner)
		}
		copy.ObjectNamespace, copy.Owner, copy.ObjectRevision = relation.Namespace(), relation.Owner, relation.Revision()
		arrayName := array.Name()
		if row.Name() != relation.Name() {
			arrayName = associatedName(relation.Namespace(), relation.Name())
		}
		if !same {
			if next.SQLType.IsComposite() {
				var err error
				copy.SQLType, err = sqltype.Array(array.SQLType.ID(), arrayName, next.SQLType)
				if err != nil {
					return err
				}
			} else {
				copy.SQLType = sqltype.LookupArray(array.SQLType.ID(), arrayName, next.SQLType)
			}
		}
		return replace(&copy)
	}
	return nil
}
