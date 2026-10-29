package extension

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func ownerIdentityKey(m MemberOwner) string {
	return m.Kind + "\x00" + m.Schema + "\x00" + m.Name + "\x00" + m.Signature
}
func validOwnerIdentity(m MemberOwner) bool {
	if m.Name == "" {
		return false
	}
	global := false
	switch m.Kind {
	case "procedure", "type", "relation", "operator", "operator_class", "operator_family", "parser", "template", "dictionary", "configuration", "collation", "conversion":
	case "schema", "language", "access_method", "event_trigger", "foreign_wrapper", "foreign_server", "cast":
		global = true
	default:
		return false
	}
	if global != (m.Schema == "") {
		return false
	}
	switch m.Kind {
	case "procedure", "operator", "operator_class", "operator_family", "cast":
	default:
		if m.Signature != "" {
			return false
		}
	}
	return true
}

// OwnerIdentity uses final SQL identity, including portable type spellings in
// overloaded identities. Numeric destination-local OIDs never cross the codec.
func OwnerIdentity(view catalog.Read, d catalog.Descriptor) (MemberOwner, error) {
	var m MemberOwner
	if d == nil {
		return m, InvalidProviderDescriptor()
	}
	m.Name = d.Name()
	if d.Namespace() != (ids.ObjectAddress{}) {
		ns, ok := view.Namespace(d.Namespace())
		if !ok {
			return m, InvalidProviderDescriptor()
		}
		m.Schema = ns.Name()
	}
	typeName := func(oid ids.OID) string {
		if oid == 0 {
			return "NONE"
		}
		return catalog.TypeSpelling(view, nil, oid)
	}
	switch d := d.(type) {
	case *catalog.Procedure:
		m.Kind = "procedure"
		if d.Signature != "" {
			var names []string
			for raw := range strings.SplitSeq(string(d.Signature), ",") {
				oid, err := strconv.ParseUint(raw, 10, 32)
				if err != nil {
					return m, err
				}
				if _, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oid)}); !ok {
					return m, InvalidProviderDescriptor()
				}
				names = append(names, typeName(ids.OID(oid)))
			}
			m.Signature = strings.Join(names, ", ")
		}
	case *catalog.Type:
		m.Kind = "type"
	case *catalog.Relation:
		m.Kind = "relation"
	case *catalog.Operator:
		m.Kind = "operator"
		m.Signature = typeName(d.Left) + ", " + typeName(d.Right)
	case *catalog.OperatorClass:
		m.Kind = "operator_class"
		method, ok := view.AccessMethod(d.Method)
		if !ok {
			return m, InvalidProviderDescriptor()
		}
		m.Signature = method.Name()
	case *catalog.OperatorFamily:
		m.Kind = "operator_family"
		method, ok := view.AccessMethod(d.Method)
		if !ok {
			return m, InvalidProviderDescriptor()
		}
		m.Signature = method.Name()
	case *catalog.TSParser:
		m.Kind = "parser"
	case *catalog.TSTemplate:
		m.Kind = "template"
	case *catalog.TSDictionary:
		m.Kind = "dictionary"
	case *catalog.TSConfiguration:
		m.Kind = "configuration"
	case *catalog.Collation:
		m.Kind = "collation"
	case *catalog.Conversion:
		m.Kind = "conversion"
	case *catalog.Namespace:
		m.Kind = "schema"
	case *catalog.Language:
		m.Kind = "language"
	case *catalog.AccessMethod:
		m.Kind = "access_method"
	case *catalog.EventTrigger:
		m.Kind = "event_trigger"
	case *catalog.ForeignObject:
		if d.Address().ClassID == ids.ForeignWrapperClassID {
			m.Kind = "foreign_wrapper"
		} else if d.Address().ClassID == ids.ForeignServerClassID {
			m.Kind = "foreign_server"
		}
	case *catalog.Cast:
		m.Kind = "cast"
		m.Name = typeName(ids.OID(d.Source))
		m.Signature = typeName(ids.OID(d.Target))
	}
	if !validOwnerIdentity(m) {
		return m, fmt.Errorf("stuntgres: invalid symbolic extension member identity")
	}
	return m, nil
}

// MemberSQL renders the ordinary ALTER EXTENSION object alternative. It is not
// an executor and carries no authority beyond the real SQL owner path.
func MemberSQL(view catalog.Read, d catalog.Descriptor) (string, error) {
	m, err := OwnerIdentity(view, d)
	if err != nil {
		return "", err
	}
	name := catalog.QuoteIdentifier(m.Name)
	if m.Schema != "" {
		name = catalog.QuoteIdentifier(m.Schema) + "." + name
	}
	switch m.Kind {
	case "procedure":
		p := d.(*catalog.Procedure)
		kind := "FUNCTION"
		if p.RoutineKind == catalog.ProcedureRoutine {
			kind = "PROCEDURE"
		} else if p.RoutineKind == catalog.AggregateRoutine {
			kind = "AGGREGATE"
		}
		return kind + " " + name + "(" + m.Signature + ")", nil
	case "operator":
		name = catalog.QuoteIdentifier(m.Schema) + "." + m.Name
		return "OPERATOR " + name + "(" + m.Signature + ")", nil
	case "operator_class":
		return "OPERATOR CLASS " + name + " USING " + catalog.QuoteIdentifier(m.Signature), nil
	case "operator_family":
		return "OPERATOR FAMILY " + name + " USING " + catalog.QuoteIdentifier(m.Signature), nil
	case "cast":
		return "CAST (" + m.Name + " AS " + m.Signature + ")", nil
	case "relation":
		r := d.(*catalog.Relation)
		kind := "TABLE"
		switch r.RelationKind {
		case catalog.ViewRelation:
			kind = "VIEW"
		case catalog.MaterializedViewRelation:
			kind = "MATERIALIZED VIEW"
		case catalog.SequenceRelation:
			kind = "SEQUENCE"
		case catalog.ForeignRelation:
			kind = "FOREIGN TABLE"
		}
		return kind + " " + name, nil
	}
	kinds := map[string]string{"type": "TYPE", "parser": "TEXT SEARCH PARSER", "template": "TEXT SEARCH TEMPLATE", "dictionary": "TEXT SEARCH DICTIONARY", "configuration": "TEXT SEARCH CONFIGURATION", "collation": "COLLATION", "conversion": "CONVERSION", "schema": "SCHEMA", "language": "LANGUAGE", "access_method": "ACCESS METHOD", "event_trigger": "EVENT TRIGGER", "foreign_wrapper": "FOREIGN DATA WRAPPER", "foreign_server": "SERVER"}
	return kinds[m.Kind] + " " + name, nil
}
