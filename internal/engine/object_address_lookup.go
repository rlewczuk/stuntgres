package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func objectLookupPath(scope *StatementScope, names []string) (analyze.NamespaceSelection, error) {
	if len(names) == 1 {
		return analyze.NamespaceSelection{Path: scope.inquiry.deparsePath}, nil
	}
	if len(names) > 3 {
		return analyze.NamespaceSelection{}, sqlError("42601", "improper qualified name (too many dotted names): "+strings.Join(names, "."))
	}
	if len(names) == 3 {
		if names[0] != scope.op.Session.DatabaseName {
			return analyze.NamespaceSelection{}, sqlError("0A000", "cross-database references are not implemented: "+strings.Join(names, "."))
		}
		names = names[1:]
	}
	auth := scope.inquiry.deparseAuth
	if auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: names[0]}) == (ids.ObjectAddress{}) && names[0] != "pg_temp" {
		return analyze.NamespaceSelection{}, sqlError("3F000", fmt.Sprintf("schema %q does not exist", names[0]))
	}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: names[0]}, {Text: names[1]}}}
	return auth.engine.lookupNamespacesSnapshot(auth, name, analyze.FunctionLookup)
}
func objectLookupDescriptor(scope *StatementScope, class ids.OID, names []string) (catalog.Descriptor, error) {
	path, err := objectLookupPath(scope, names)
	if err != nil {
		return nil, err
	}
	for i := range path.Count() {
		a := scope.inquiry.commentCatalog.NameAddress(ids.NameKey{ClassID: class, Namespace: path.At(i), Name: names[len(names)-1]})
		if d := scope.inquiry.commentCatalog.Get(a); d != nil {
			return d, nil
		}
	}
	return nil, nil
}
func lookupObjectAddress(ctx context.Context, scope *StatementScope, values [3]sqltype.Value) (ids.ObjectAddress, error) {
	kind, err := values[0].Text()
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	class, err := regobj.ObjectInputClass(kind)
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	typeName := kind == "type" || kind == "cast" || kind == "domain constraint" || class == 3576
	if (typeName || class == 2613) && values[1].ArrayLen() != 1 {
		return ids.ObjectAddress{}, regobj.ObjectListLength("name", 1, true)
	}
	if class == 2613 && values[1].ArrayElement(0).IsNull() {
		return ids.ObjectAddress{}, sqlError("22023", "large object OID may not be null")
	}
	names, err := regobj.ObjectInputArray(values[1])
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	if len(names) == 0 {
		return ids.ObjectAddress{}, regobj.ObjectListLength("name", 1, false)
	}
	view := scope.inquiry.commentCatalog
	types := capturedRegtypeInput{capture: regobj.InputCapture{
		Authority: inputAuthority(scope.inquiry.deparseAuth), Path: scope.inquiry.deparsePath,
		Database: scope.op.Session.DatabaseName, Token: scope.op.Session.SessionToken, Temporary: temporaryNamespace(scope.inquiry.deparseAuth),
	}, op: scope.op}
	parseSyntax := func(input string) (parser.TypeName, error) {
		return parser.ParseTypeName(ctx, input, func(notice sqlstate.Error) { scope.op.EmitNotice(session.LevelNotice, notice) })
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
		args, err = regobj.ObjectInputArray(values[2])
		if err != nil {
			return ids.ObjectAddress{}, err
		}
	}
	if (class == 1418 || class == 6237) && len(names) != 1 {
		return ids.ObjectAddress{}, regobj.ObjectListLength("name", 1, true)
	}
	if (kind == "cast" || kind == "domain constraint" || class == 1418 || class == 6237 || class == 6106 || class == 826 || class == 3576) && len(args) != 1 {
		return ids.ObjectAddress{}, regobj.ObjectListLength("argument", 1, true)
	}
	if (class == 2616 || class == 2753) && len(names) < 2 {
		return ids.ObjectAddress{}, regobj.ObjectListLength("name", 2, false)
	}
	if (class == 2602 || class == 2603) && len(names) < 3 {
		return ids.ObjectAddress{}, regobj.ObjectListLength("name", 3, false)
	}
	if (class == 2617 || class == 2602 || class == 2603) && len(args) != 2 {
		return ids.ObjectAddress{}, regobj.ObjectListLength("argument", 2, true)
	}
	switch class {
	case 2612, 2601, 1262, 3466, 3079, 2328, 1417, 6243, 6104, 1260, 2615, 6100, 1213:
		if len(names) != 1 {
			return ids.ObjectAddress{}, regobj.ObjectListLength("name", 1, true)
		}
	}
	if typeName {
		typeID, err = types.resolveParsedTypeName(ctx, parsedName, scope.op)
		if err != nil {
			return ids.ObjectAddress{}, err
		}
	}
	var argIDs []ids.OID
	if parsedArgs != nil {
		argIDs = make([]ids.OID, len(parsedArgs))
		for i, raw := range parsedArgs {
			id, err := types.resolveParsedTypeName(ctx, raw, scope.op)
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
	case 1247:
		a.ObjectID = ids.OID(typeID)
		return a, nil
	case 2615, 1260, 1262, 1213, 3079:
		name := names[0]
		switch class {
		case 2615:
			a = view.NameAddress(ids.NameKey{ClassID: 2615, Name: name})
		case 1260:
			if role, ok := scope.inquiry.commentRoles.Lookup(name); ok {
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
		path, err := objectLookupPath(scope, names)
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
			human[i] = regobj.TypeSpelling(view, scope.inquiry.deparsePath, id)
		}
		p, count := regobj.SelectExactRoutine(view, path, names[len(names)-1], catalog.ProcedureSignature(signature.String()))
		spelling := make([]string, len(names))
		for i, name := range names {
			spelling[i] = catalog.Identifier(name)
		}
		sig := strings.Join(spelling, ".") + "(" + strings.Join(human, ", ") + ")"
		if count == 0 {
			noun := "function"
			if kind == "procedure" {
				noun = "procedure"
			}
			return missing("42883", noun+" "+sig+" does not exist")
		}
		actual := routine.ObjectRoutineKind(p.ID)
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
		path, err := objectLookupPath(scope, names)
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
		return missing("42704", fmt.Sprintf("cast from type %s to type %s does not exist", regobj.TypeSpelling(view, scope.inquiry.deparsePath, ids.OID(typeID)), regobj.TypeSpelling(view, scope.inquiry.deparsePath, argIDs[0])))
	case 2616, 2753:
		method := uint32(0)
		switch names[0] {
		case "btree":
			method = 403
		case "hash":
			method = 405
		default:
			return missing("42704", fmt.Sprintf("access method %q does not exist", names[0]))
		}
		path, err := objectLookupPath(scope, names[1:])
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		for i := range path.Count() {
			if path.At(i).ObjectID != ids.PGCatalogNamespaceOID {
				continue
			}
			for j := range sqltype.IndexClassMetadataCount() {
				cl, _ := sqltype.IndexClassMetadataAt(j)
				name, id, am := cl.Name, cl.OID, cl.Method
				if class == 2753 {
					family, ok := sqltype.IndexFamilyMetadataByOID(cl.Family)
					if !ok {
						continue
					}
					name, id, am = family.Name, family.OID, family.Method
				}
				if method == am && name == names[len(names)-1] {
					a.ObjectID = ids.OID(id)
					return a, nil
				}
			}
		}
		return missing("42704", fmt.Sprintf("%s %q for access method %q does not exist", kind, strings.Join(names[1:], "."), names[0]))
	case 2606:
		if kind == "domain constraint" {
			if domain, ok := view.Type(ids.ObjectAddress{ClassID: 1247, ObjectID: ids.OID(typeID)}); ok {
				for _, check := range domain.DomainChecks {
					if check.Name == args[0] {
						a.ObjectID = check.OID
						return a, nil
					}
				}
			}
			return missing("42704", fmt.Sprintf("constraint %q for domain %s does not exist", args[0], regobj.TypeSpelling(view, scope.inquiry.deparsePath, ids.OID(typeID))))
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
		d, err := objectLookupDescriptor(scope, 1259, relationNames)
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		if d == nil {
			return missing("42P01", fmt.Sprintf("relation %q does not exist", strings.Join(relationNames, ".")))
		}
		relation := d.(*catalog.Relation)
		if class == 1259 {
			if !child {
				actual := regobj.ObjectRelationKind(view, relation)
				if actual != kind {
					return missing("42809", fmt.Sprintf("%q is not a%s %s", relation.Name(), map[string]string{"index": "n"}[kind], kind))
				}
				return relation.Address(), nil
			}
			column := names[len(names)-1]
			for _, sub := range relation.Attributes {
				if regobj.ObjectAttributeName(view, relation, sub) == column {
					return ids.ObjectAddress{1259, relation.Address().ObjectID, sub}, nil
				}
			}
			for sub := int32(-1); sub >= -6; sub-- {
				if regobj.ObjectAttributeName(view, relation, sub) == column {
					return ids.ObjectAddress{1259, relation.Address().ObjectID, sub}, nil
				}
			}
			return missing("42703", fmt.Sprintf("column %q of relation %q does not exist", column, strings.Join(relationNames, ".")))
		}
		name := names[len(names)-1]
		id := regobj.OwnedAddressByName(relation, class, name)
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
		d, err := objectLookupDescriptor(scope, class, names)
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
