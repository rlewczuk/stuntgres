package regobj

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// AddressLookup is what pg_get_object_address reads: the statement's comment
// catalog and roles, its deparse capture (authority, reference path, database,
// session token and TEMP namespace), and two root capabilities.
type AddressLookup struct {
	Catalog catalog.Read
	Roles   authorization.Read
	Capture InputCapture
	// Namespace selects the namespace of a two-part name with function lookup
	// rules, after the schema is known to exist.
	Namespace func(parser.ObjectName) (analyze.NamespaceSelection, error)
	// Notice receives type-name parser notices and type-modifier warnings. A
	// nil Notice drops them.
	Notice func(session.NoticeLevel, sqlstate.Error)
}

func (q AddressLookup) path(names []string) (analyze.NamespaceSelection, error) {
	if len(names) == 1 {
		return analyze.NamespaceSelection{Path: q.Capture.Path}, nil
	}
	if len(names) > 3 {
		return analyze.NamespaceSelection{}, sqlError("42601", "improper qualified name (too many dotted names): "+strings.Join(names, "."))
	}
	if len(names) == 3 {
		if names[0] != q.Capture.Database {
			return analyze.NamespaceSelection{}, sqlError("0A000", "cross-database references are not implemented: "+strings.Join(names, "."))
		}
		names = names[1:]
	}
	a := q.Capture.Authority
	if catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.NamespaceClassID, Name: names[0]}) == (ids.ObjectAddress{}) && names[0] != "pg_temp" {
		return analyze.NamespaceSelection{}, sqlError("3F000", fmt.Sprintf("schema %q does not exist", names[0]))
	}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: names[0]}, {Text: names[1]}}}
	return q.Namespace(name)
}
func (q AddressLookup) descriptor(class ids.OID, names []string) (catalog.Descriptor, error) {
	path, err := q.path(names)
	if err != nil {
		return nil, err
	}
	for i := range path.Count() {
		a := q.Catalog.NameAddress(ids.NameKey{ClassID: class, Namespace: path.At(i), Name: names[len(names)-1]})
		if d := q.Catalog.Get(a); d != nil {
			return d, nil
		}
	}
	return nil, nil
}

// Lookup resolves pg_get_object_address(type, names, args).
func (q AddressLookup) Lookup(ctx context.Context, values [3]sqltype.Value) (ids.ObjectAddress, error) {
	kind, err := values[0].Text()
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	class, err := ObjectInputClass(kind)
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	typeName := kind == "type" || kind == "cast" || kind == "domain constraint" || class == 3576
	if (typeName || class == 2613) && values[1].ArrayLen() != 1 {
		return ids.ObjectAddress{}, ObjectListLength("name", 1, true)
	}
	if class == 2613 && values[1].ArrayElement(0).IsNull() {
		return ids.ObjectAddress{}, sqlError("22023", "large object OID may not be null")
	}
	names, err := ObjectInputArray(values[1])
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	if len(names) == 0 {
		return ids.ObjectAddress{}, ObjectListLength("name", 1, false)
	}
	view := q.Catalog
	types := TypeInput{capture: q.Capture}
	notices := typeNotices(q.Notice)
	parseSyntax := func(input string) (parser.TypeName, error) {
		return parser.ParseTypeName(ctx, input, func(notice sqlstate.Error) {
			if notices != nil {
				notices(session.LevelNotice, notice)
			}
		})
	}
	var typeID uint32
	var parsedName parser.TypeName
	if typeName {
		parsedName, err = parseSyntax(names[0])
		if err != nil {
			return ids.ObjectAddress{}, err
		}
	}
	var args []string
	var parsedArgs []parser.TypeName
	if class == 1255 || class == 2617 || class == 2605 || class == 2602 || class == 2603 {
		args = make([]string, values[2].ArrayLen())
		parsedArgs = make([]parser.TypeName, len(args))
		// Type arguments are checked and parsed one element at a time.
		// A syntax error must precede a NULL in a later element.
		for i := range args {
			cell := values[2].ArrayElement(i)
			if cell.IsNull() {
				return ids.ObjectAddress{}, sqlError("22023", "name or argument lists may not contain nulls")
			}
			args[i], err = cell.Text()
			if err != nil {
				return ids.ObjectAddress{}, err
			}
			parsedArgs[i], err = parseSyntax(args[i])
			if err != nil {
				return ids.ObjectAddress{}, err
			}
		}
	} else {
		args, err = ObjectInputArray(values[2])
		if err != nil {
			return ids.ObjectAddress{}, err
		}
	}
	if (class == 1418 || class == 6237) && len(names) != 1 {
		return ids.ObjectAddress{}, ObjectListLength("name", 1, true)
	}
	if (kind == "cast" || kind == "domain constraint" || class == 1418 || class == 6237 || class == 6106 || class == 826 || class == 3576) && len(args) != 1 {
		return ids.ObjectAddress{}, ObjectListLength("argument", 1, true)
	}
	if (class == 2616 || class == 2753) && len(names) < 2 {
		return ids.ObjectAddress{}, ObjectListLength("name", 2, false)
	}
	if (class == 2602 || class == 2603) && len(names) < 3 {
		return ids.ObjectAddress{}, ObjectListLength("name", 3, false)
	}
	if (class == 2617 || class == 2602 || class == 2603) && len(args) != 2 {
		return ids.ObjectAddress{}, ObjectListLength("argument", 2, true)
	}
	switch class {
	case 2612, 2601, 1262, 3466, 3079, 2328, 1417, 6243, 6104, 1260, 2615, 6100, 1213:
		if len(names) != 1 {
			return ids.ObjectAddress{}, ObjectListLength("name", 1, true)
		}
	}
	if class == ids.ProcedureClassID && len(parsedArgs) > 100 {
		noun := "functions"
		if kind == "procedure" {
			noun = "procedures"
		}
		return ids.ObjectAddress{}, sqlError("54023", noun+" cannot have more than 100 arguments")
	}
	if typeName {
		typeID, err = types.resolveParsed(ctx, parsedName, notices)
		if err != nil {
			return ids.ObjectAddress{}, err
		}
	}
	var argIDs []ids.OID
	if parsedArgs != nil {
		argIDs = make([]ids.OID, len(parsedArgs))
		for i, raw := range parsedArgs {
			id, err := types.resolveParsed(ctx, raw, notices)
			if err != nil {
				return ids.ObjectAddress{}, err
			}
			argIDs[i] = ids.OID(id)
		}
	}
	a := ids.ObjectAddress{ClassID: class}
	missing := func(code, message string) (ids.ObjectAddress, error) {
		return ids.ObjectAddress{}, sqlError(code, message)
	}
	switch class {
	case ids.SubscriptionClassID:
		address := view.NameAddress(catalog.SubscriptionNameKey(view.Owner().DatabaseAddress(), names[0]))
		if address != (ids.ObjectAddress{}) {
			return address, nil
		}
		return missing("42704", fmt.Sprintf(`subscription "%s" does not exist`, names[0]))
	case ids.AccessMethodClassID:
		if m, ok := view.LookupAccessMethod(names[0]); ok {
			return m.Address(), nil
		}
		return missing("42704", fmt.Sprintf(`access method "%s" does not exist`, names[0]))
	case 1247:
		a.ObjectID = ids.OID(typeID)
		return a, nil
	case 2615, 1260, 1262, 1213, 3079:
		name := names[0]
		switch class {
		case 2615:
			a = view.NameAddress(ids.NameKey{ClassID: 2615, Name: name})
		case 1260:
			if role, ok := q.Roles.Lookup(name); ok {
				a.ObjectID = ids.OID(role.Ref.ID())
			}
		case 1213:
			if name == "pg_default" {
				a.ObjectID = 1663
			}
			if name == "pg_global" {
				a.ObjectID = 1664
			}
		case 1262, 3079:
			a = view.NameAddress(ids.NameKey{ClassID: class, Name: name})
		}
		if a.ObjectID != 0 {
			return a, nil
		}
		code := "42704"
		if class == 2615 {
			code = "3F000"
		}
		if class == 1262 {
			code = "3D000"
		}
		return missing(code, fmt.Sprintf("%s %q does not exist", kind, name))
	case 1255:
		path, err := q.path(names)
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		var signature strings.Builder
		human := make([]string, len(argIDs))
		for i, id := range argIDs {
			if i != 0 {
				signature.WriteByte(',')
			}
			signature.WriteString(strconv.FormatUint(uint64(id), 10))
			human[i] = TypeSpelling(view, q.Capture.Path, id)
		}
		p, count := SelectExactRoutine(view, path, names[len(names)-1], catalog.ProcedureSignature(signature.String()))
		sig := strings.Join(names, ".") + "(" + strings.Join(human, ", ") + ")"
		if count == 0 {
			noun := kind
			if kind == "aggregate" && len(argIDs) == 0 {
				sig = strings.Join(names, ".") + "(*)"
			}
			return missing("42883", noun+" "+sig+" does not exist")
		}
		actual := objectRoutineKind(view, p.ID)
		if kind == "aggregate" && actual != "aggregate" {
			return missing("42809", "function "+sig+" is not an aggregate")
		}
		if kind == "procedure" && actual != "procedure" {
			return missing("42809", sig+" is not a procedure")
		}
		if kind == "function" && actual == "procedure" {
			return missing("42809", sig+" is not a function")
		}
		a.ObjectID = p.ID
		return a, nil
	case 2617:
		path, err := q.path(names)
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		for i := range path.Count() {
			key := ids.NameKey{ClassID: 2617, Namespace: path.At(i), Name: names[len(names)-1], Signature: fmt.Sprintf("%d,%d", argIDs[0], argIDs[1])}
			if id := view.NameAddress(key); id.ObjectID != 0 {
				return id, nil
			}
		}
		return missing("42883", fmt.Sprintf("operator %s(%s, %s) does not exist", strings.Join(names, "."), args[0], args[1]))
	case 2605:
		for i := range sqltype.CastMetadataCount() {
			cast, _ := sqltype.CastMetadataAt(i)
			if uint32(cast.Source) == typeID && ids.OID(cast.Target) == argIDs[0] {
				a.ObjectID = ids.OID(cast.OID)
				return a, nil
			}
		}
		return missing("42704", fmt.Sprintf("cast from type %s to type %s does not exist", TypeSpelling(view, q.Capture.Path, ids.OID(typeID)), TypeSpelling(view, q.Capture.Path, argIDs[0])))
	case ids.AccessMethodOperatorClassID, ids.AccessMethodProcedureClassID:
		fact, ok := view.LookupAccessMethod(names[0])
		if !ok || fact.MethodKind() != "i" {
			return missing("42704", fmt.Sprintf("access method %q does not exist", names[0]))
		}
		method := fact.Address().ObjectID
		number, err := strconv.ParseInt(names[len(names)-1], 10, 16)
		if err != nil {
			return missing("22P02", fmt.Sprintf("invalid input syntax for type smallint: %q", names[len(names)-1]))
		}
		path, err := q.path(names[1 : len(names)-1])
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		for i := range path.Count() {
			family := view.NameAddress(catalog.FamilyNameKey(path.At(i), method, names[len(names)-2]))
			if family == (ids.ObjectAddress{}) {
				continue
			}
			if class == ids.AccessMethodProcedureClassID {
				if member, ok := view.FamilySupport(family.ObjectID, argIDs[0], argIDs[1], int16(number)); ok {
					return member.Address(), nil
				}
			} else {
				for _, purpose := range []byte{'s', 'o'} {
					if member, ok := view.FamilyOperator(family.ObjectID, argIDs[0], argIDs[1], int16(number), purpose); ok {
						return member.Address(), nil
					}
				}
			}
		}
		return missing("42704", fmt.Sprintf("%s %q does not exist", kind, strings.Join(names, ".")))
	case 2616, 2753:
		method, ok := view.LookupAccessMethod(names[0])
		if !ok || method.MethodKind() != "i" {
			return missing("42704", fmt.Sprintf("access method %q does not exist", names[0]))
		}
		path, err := q.path(names[1:])
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		for i := range path.Count() {
			if class == ids.OperatorFamilyClassID {
				if address := view.NameAddress(catalog.FamilyNameKey(path.At(i), method.Address().ObjectID, names[len(names)-1])); address != (ids.ObjectAddress{}) {
					return address, nil
				}
				continue
			}
			if class, ok := view.LookupOperatorClass(path.At(i), method.Address().ObjectID, names[len(names)-1]); ok {
				return class.Address(), nil
			}
		}
		return missing("42704", fmt.Sprintf("%s %q for access method %q does not exist", kind, strings.Join(names[1:], "."), names[0]))
	case 2606:
		if kind == "domain constraint" {
			if domain, ok := view.Type(ids.ObjectAddress{ClassID: 1247, ObjectID: ids.OID(typeID)}); ok {
				if domain.DomainNotNull != nil && domain.DomainNotNull.Name == args[0] {
					a.ObjectID = domain.DomainNotNull.OID
					return a, nil
				}
				for _, check := range domain.DomainChecks {
					if check.Name == args[0] {
						a.ObjectID = check.OID
						return a, nil
					}
				}
			}
			return missing("42704", fmt.Sprintf("constraint %q for domain %s does not exist", args[0], TypeSpelling(view, q.Capture.Path, ids.OID(typeID))))
		}
	}
	if class == 1259 || class == 2606 || class == 2604 || class == 2618 || class == 3256 || class == 2620 {
		relationNames := names
		child := class != 1259 || kind == "table column" || kind == "foreign table column"
		if child {
			if len(names) < 2 {
				message := "must specify relation and object name"
				if class == 1259 || class == 2604 {
					message = "column name must be qualified"
				}
				return ids.ObjectAddress{}, sqlError("42601", message)
			}
			relationNames = names[:len(names)-1]
		}
		d, err := q.descriptor(1259, relationNames)
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		if d == nil {
			return missing("42P01", fmt.Sprintf("relation %q does not exist", strings.Join(relationNames, ".")))
		}
		relation := d.(*catalog.Relation)
		if class == 1259 {
			if !child {
				actual := ObjectRelationKind(view, relation)
				if actual != kind {
					return missing("42809", fmt.Sprintf("%q is not a%s %s", relation.Name(), map[string]string{"index": "n"}[kind], kind))
				}
				return relation.Address(), nil
			}
			column := names[len(names)-1]
			for _, sub := range relation.Attributes {
				if ObjectAttributeName(view, relation, sub) == column {
					return ids.ObjectAddress{ClassID: 1259, ObjectID: relation.Address().ObjectID, SubID: sub}, nil
				}
			}
			for sub := int32(-1); sub >= -6; sub-- {
				if ObjectAttributeName(view, relation, sub) == column {
					return ids.ObjectAddress{ClassID: 1259, ObjectID: relation.Address().ObjectID, SubID: sub}, nil
				}
			}
			return missing("42703", fmt.Sprintf("column %q of relation %q does not exist", column, strings.Join(relationNames, ".")))
		}
		name := names[len(names)-1]
		id := OwnedAddressByName(relation, class, name)
		if id != 0 {
			a.ObjectID = id
			return a, nil
		}
		if class == 2604 {
			return missing("42703", fmt.Sprintf("default value for column %q of relation %q does not exist", name, strings.Join(relationNames, ".")))
		}
		object, owner := kind, "table"
		if class == 2606 {
			object = "constraint"
		}
		if class == 2618 {
			owner = "relation"
		}
		return missing("42704", fmt.Sprintf("%s %q for %s %q does not exist", object, name, owner, relation.Name()))
	}
	if class == 3456 || class == 2607 || class == 3764 || class == 3600 {
		d, err := q.descriptor(class, names)
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		if d != nil {
			return d.Address(), nil
		}
		message := fmt.Sprintf("%s %q does not exist", kind, strings.Join(names, "."))
		if class == 3456 {
			message = fmt.Sprintf("collation %q for encoding \"UTF8\" does not exist", strings.Join(names, "."))
		}
		return missing("42704", message)
	}
	if class == 2613 {
		oid, _ := view.LookupType(sqltype.Oid)
		value, err := oid.Input(ctx, names[0], -1)
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		id, err := value.OID()
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		return missing("42704", fmt.Sprintf("large object %d does not exist", id))
	}
	// Classes without an owned provider still use released symbolic-missing
	// diagnostics. No descriptor or OID is manufactured to satisfy lookup.
	return missing("42704", fmt.Sprintf("%s %q does not exist", kind, strings.Join(names, ".")))
}
