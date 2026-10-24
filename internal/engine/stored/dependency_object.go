package stored

import (
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// DependencyObject describes target the way PostgreSQL's getObjectDescription
// does in dependency diagnostics, qualifying names not visible on path. A nil
// path (an internal provider stage) never qualifies.
func DependencyObject(read catalog.Read, target ids.ObjectAddress, path []ids.ObjectAddress) string {
	base := target
	base.SubID = 0
	if d, ok := read.Find(base); ok {
		if cast, ok := d.(*catalog.Cast); ok {
			source := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(cast.Source)})
			target := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(cast.Target)})
			return "cast from " + catalog.TypeDiagnosticNameAt(read, nil, source, path) + " to " + catalog.TypeDiagnosticNameAt(read, nil, target, path)
		}
		if description, family := regobj.OperatorFamilyDescription(read, d, path); family {
			return description
		}
	}
	if parent := ConstraintParent(read, base); parent != nil {
		return "constraint " + catalog.DependencyIdentifier(ConstraintName(parent, base)) + " on table " + DependencyRelationName(read, parent, path)
	}
	if d, ok := read.Namespace(base); ok {
		return "schema " + catalog.DependencyIdentifier(d.Name())
	}
	if d, ok := read.Database(base); ok {
		return "database " + catalog.DependencyIdentifier(d.Name())
	}
	if d, ok := read.Foreign(base); ok {
		if d.Address().ClassID == ids.ForeignMappingClassID {
			user := "public"
			if d.MappedRole != (authorization.PrincipalRef{}) {
				user = catalog.DependencyIdentifier(d.MappedName)
			}
			server := read.Get(d.Server)
			return "user mapping for " + user + " on server " + catalog.DependencyIdentifier(server.Name())
		}
		return catalog.ForeignLabel(d.Address().ClassID) + " " + catalog.DependencyIdentifier(d.Name())
	}
	if d, ok := read.Extension(base); ok {
		return "extension " + catalog.DependencyIdentifier(d.Name())
	}
	if _, ok := read.Operator(base); ok {
		return "operator " + regobj.OperatorSpelling(read, path, regobj.CollectOperatorGroups(read), base.ObjectID, sqltype.Regoperator)
	}
	if d, ok := read.Procedure(base); ok {
		name := catalog.DependencyIdentifier(d.Name())
		visible := path == nil
		for _, namespace := range path {
			if n, ok := read.Namespace(namespace); ok && n.Temporary != (ids.SessionToken{}) {
				continue
			}
			if address, ok := read.LookupName(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: namespace, Name: d.Name(), Signature: string(d.Signature)}); ok {
				visible = address == d.Address()
				break
			}
		}
		if !visible {
			name = catalog.DependencyIdentifier(read.Get(d.Namespace()).Name()) + "." + name
		}
		var text strings.Builder
		text.WriteString("function ")
		text.WriteString(name)
		text.WriteByte('(')
		regobj.WriteRoutineArguments(&text, read, nil, path, d.Signature, true)
		text.WriteByte(')')
		return text.String()
	}
	if d, ok := read.TSDictionary(base); ok {
		name := catalog.DependencyIdentifier(d.Name())
		visible := path == nil
		for _, ns := range path {
			if n, ok := read.Namespace(ns); ok && n.Temporary != (ids.SessionToken{}) {
				continue
			}
			if address, ok := read.LookupName(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: d.Name()}); ok {
				visible = address == d.Address()
				break
			}
		}
		if !visible {
			name = catalog.DependencyIdentifier(read.Get(d.Namespace()).Name()) + "." + name
		}
		return "text search dictionary " + name
	}
	if t, ok := read.Type(base); ok {
		return "type " + catalog.TypeDiagnosticNameAt(read, nil, t, path)
	}
	if d, ok := read.Collation(base); ok {
		name := catalog.DependencyIdentifier(d.Name())
		if path != nil {
			visible := false
			for _, ns := range path {
				if namespace, ok := read.Namespace(ns); ok && namespace.Temporary != (ids.SessionToken{}) {
					continue
				}
				if a, ok := read.LookupName(ids.NameKey{ClassID: ids.CollationClassID, Namespace: ns, Name: d.Name()}); ok {
					visible = a == d.Address()
					break
				}
			}
			if !visible {
				name = catalog.DependencyIdentifier(read.Get(d.Namespace()).Name()) + "." + name
			}
		}
		return "collation " + name
	}
	if d, ok := read.Conversion(base); ok {
		name := catalog.DependencyIdentifier(d.Name())
		if path != nil {
			visible := false
			for _, ns := range path {
				if namespace, ok := read.Namespace(ns); ok && namespace.Temporary != (ids.SessionToken{}) {
					continue
				}
				if a, ok := read.LookupName(ids.NameKey{ClassID: ids.ConversionClassID, Namespace: ns, Name: d.Name()}); ok {
					visible = a == d.Address()
					break
				}
			}
			if !visible {
				name = catalog.DependencyIdentifier(read.Get(d.Namespace()).Name()) + "." + name
			}
		}
		return "conversion " + name
	}
	if base.ClassID == ids.AccessMethodClassID {
		if d, ok := read.AccessMethod(base.ObjectID); ok {
			return "access method " + catalog.DependencyIdentifier(d.Name())
		}
	}
	d := read.MustRelation(base)
	if d.SQLIndex != nil && d.SQLIndex.Constraint != nil {
		parent := read.MustRelation(d.SQLIndex.Parent)
		return "constraint " + catalog.DependencyIdentifier(d.SQLIndex.Constraint.Name) + " on table " + DependencyRelationName(read, parent, path)
	}
	object := catalog.RelationDiagnosticKind(d) + " " + DependencyRelationName(read, d, path)
	if target.SubID != 0 {
		for i, attribute := range d.Attributes {
			if attribute == target.SubID {
				return "column " + d.Columns[i].Name + " of " + object
			}
		}
	}
	return object
}

// DependencyRelationName is the diagnostic name of relation d: bare without
// a path, otherwise qualified when d is not visible on path.
func DependencyRelationName(read catalog.Read, d *catalog.Relation, path []ids.ObjectAddress) string {
	// Internal provider stages have no SQL visibility context. SQL utilities
	// supply their actual lookup path, including an empty effective path.
	if path == nil {
		return catalog.DependencyIdentifier(d.Name())
	}
	return RelationDiagnosticNameAt(read, d, path)
}
