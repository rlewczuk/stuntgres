package regobj

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ResolveExtensionMember shares the address resolver's coherent path and real
// descriptor identities, without its SQL-array input or a SQL-text round trip.
func ResolveExtensionMember(h routine.DDLHost, auth ddl.Authority, target parser.ExtensionMemberTarget, database string) (catalog.Descriptor, error) {
	view := h.AccessView(auth)
	if target.Kind == "function" || target.Kind == "procedure" || target.Kind == "routine" || target.Kind == "aggregate" {
		p, _, err := routine.ResolveUtilityRoutine(h, auth, target.Kind, target.Routine)
		return p, err
	}
	if target.Kind == "type" || target.Kind == "domain" {
		oid, err := h.ResolveRegtype(auth, target.Type)
		if err != nil {
			return nil, err
		}
		t := view.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: oid})
		if target.Kind == "domain" && !t.SQLType.IsDomain() {
			path, _ := auth.ReferencePath()
			return nil, sqlError("42809", catalog.TypeDiagnosticNameAt(view.Catalog, nil, t, path)+" is not a domain")
		}
		return t, nil
	}
	path, err := auth.ReferencePath()
	if err != nil {
		return nil, err
	}
	q := AddressLookup{Catalog: view.Catalog, Roles: view.Roles, Capture: InputCapture{Authority: Authority{View: view, Environment: auth}, Path: path, Database: database}, Namespace: func(name parser.ObjectName) (analyze.NamespaceSelection, error) {
		ns, err := h.SearchNamespaces(auth, name, ids.ProcedureClassID)
		return analyze.NamespaceSelection{Path: ns}, err
	}}
	if target.Kind == "cast" {
		source, err := h.ResolveRegtype(auth, target.Type)
		if err != nil {
			return nil, err
		}
		destination, err := h.ResolveRegtype(auth, target.TargetType)
		if err != nil {
			return nil, err
		}
		if cast, ok := view.Catalog.Cast(sqltype.ID(source), sqltype.ID(destination)); ok {
			return cast, nil
		}
		return nil, sqlError("42704", fmt.Sprintf("cast from type %s to type %s does not exist", TypeSpelling(view.Catalog, path, source), TypeSpelling(view.Catalog, path, destination)))
	}
	name := target.Name
	if target.Kind == "operator" {
		name = target.Operator.Name
	}
	var parts [3]string
	names := parts[:0]
	for _, part := range name.Parts {
		names = append(names, part.Text)
	}
	var class ids.OID
	global := false
	switch target.Kind {
	case "table", "foreign table", "view", "materialized view", "sequence":
		class = ids.RelationClassID
	case "operator":
		class = ids.OperatorClassID
	case "operator class":
		class = ids.OperatorClassCatalogID
	case "operator family":
		class = ids.OperatorFamilyClassID
	case "text search parser":
		class = ids.TSParserClassID
	case "text search template":
		class = ids.TSTemplateClassID
	case "text search dictionary":
		class = ids.TSDictionaryClassID
	case "text search configuration":
		class = ids.TSConfigurationClassID
	case "collation":
		class = ids.CollationClassID
	case "conversion":
		class = ids.ConversionClassID
	case "schema":
		class = ids.NamespaceClassID
		global = true
	case "language":
		class = ids.LanguageClassID
		global = true
	case "access method":
		class = ids.AccessMethodClassID
		global = true
	case "event trigger":
		class = ids.EventTriggerClassID
		global = true
	case "foreign-data wrapper":
		class = ids.ForeignWrapperClassID
		global = true
	case "server":
		class = ids.ForeignServerClassID
		global = true
	default:
		return nil, fmt.Errorf("stuntgres: invalid extension member kind %q", target.Kind)
	}
	var d catalog.Descriptor
	if global {
		d = view.Catalog.Get(view.Catalog.NameAddress(ids.NameKey{ClassID: class, Name: names[0]}))
	} else if target.Kind == "operator" {
		var operands [2]ids.OID
		for i, raw := range [2]parser.TypeName{target.Operator.Left, target.Operator.Right} {
			if raw.Name == "none" && !raw.Quoted && raw.Schema == nil {
				continue
			}
			operands[i], err = h.ResolveRegtype(auth, raw)
			if err != nil {
				return nil, err
			}
		}
		ns, err := q.path(names)
		if err != nil {
			return nil, err
		}
		for i := range ns.Count() {
			a := view.Catalog.NameAddress(ids.NameKey{ClassID: class, Namespace: ns.At(i), Name: names[len(names)-1], Signature: fmt.Sprintf("%d,%d", operands[0], operands[1])})
			if d = view.Catalog.Get(a); d != nil {
				break
			}
		}
		if d == nil {
			return nil, sqlError("42883", fmt.Sprintf("operator %s(%s, %s) does not exist", catalog.ObjectNameText(name), TypeSpelling(view.Catalog, path, operands[0]), TypeSpelling(view.Catalog, path, operands[1])))
		}
	} else if target.Kind == "operator class" || target.Kind == "operator family" {
		method, ok := view.Catalog.LookupAccessMethod(target.Method.Text)
		if !ok || method.MethodKind() != "i" {
			return nil, sqlError("42704", fmt.Sprintf("access method %q does not exist", target.Method.Text))
		}
		ns, err := q.path(names)
		if err != nil {
			return nil, err
		}
		for i := range ns.Count() {
			if target.Kind == "operator family" {
				d = view.Catalog.Get(view.Catalog.NameAddress(catalog.FamilyNameKey(ns.At(i), method.Address().ObjectID, names[len(names)-1])))
			} else if c, ok := view.Catalog.LookupOperatorClass(ns.At(i), method.Address().ObjectID, names[len(names)-1]); ok {
				d = c
			}
			if d != nil {
				break
			}
		}
		if d == nil {
			return nil, sqlError("42704", fmt.Sprintf("%s %q for access method %q does not exist", target.Kind, catalog.ObjectNameText(name), target.Method.Text))
		}
	} else {
		d, err = q.descriptor(class, names)
		if err != nil {
			return nil, err
		}
	}
	if d == nil {
		code := "42704"
		noun := target.Kind
		if class == ids.RelationClassID {
			code = "42P01"
			noun = "relation"
		}
		if class == ids.NamespaceClassID {
			code = "3F000"
		}
		return nil, sqlError(code, fmt.Sprintf("%s %q does not exist", noun, catalog.ObjectNameText(name)))
	}
	if r, ok := d.(*catalog.Relation); ok {
		matches := target.Kind == "table" && (r.RelationKind == catalog.TableRelation || r.RelationKind == catalog.PartitionedRelation || r.RelationKind == catalog.CatalogRelation) || target.Kind == "foreign table" && r.RelationKind == catalog.ForeignRelation || target.Kind == "view" && r.RelationKind == catalog.ViewRelation || target.Kind == "materialized view" && r.RelationKind == catalog.MaterializedViewRelation || target.Kind == "sequence" && r.RelationKind == catalog.SequenceRelation
		if !matches {
			return nil, sqlError("42809", fmt.Sprintf("%q is not a %s", catalog.ObjectNameText(name), target.Kind))
		}
	}
	return d, nil
}
