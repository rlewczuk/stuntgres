package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func objectInputClass(kind string) (ids.OID, error) {
	switch kind {
	case "table", "index", "sequence", "view", "materialized view", "foreign table", "table column", "foreign table column":
		return 1259, nil
	case "function", "aggregate", "procedure":
		return 1255, nil
	case "type":
		return 1247, nil
	case "schema":
		return 2615, nil
	case "collation":
		return 3456, nil
	case "conversion":
		return 2607, nil
	case "extension":
		return 3079, nil
	case "text search template":
		return 3764, nil
	case "text search dictionary":
		return 3600, nil
	case "database":
		return 1262, nil
	case "role":
		return 1260, nil
	case "table constraint", "domain constraint":
		return 2606, nil
	case "default value":
		return 2604, nil
	case "rule":
		return 2618, nil
	case "policy":
		return 3256, nil
	case "tablespace":
		return 1213, nil
	case "cast":
		return 2605, nil
	case "operator class":
		return 2616, nil
	case "operator family":
		return 2753, nil
	case "operator":
		return 2617, nil
	case "language":
		return 2612, nil
	case "access method":
		return 2601, nil
	case "foreign-data wrapper":
		return 2328, nil
	case "server":
		return 1417, nil
	case "user mapping":
		return 1418, nil
	case "trigger":
		return 2620, nil
	case "event trigger":
		return 3466, nil
	case "publication":
		return 6104, nil
	case "subscription":
		return 6100, nil
	case "statistics object":
		return 3381, nil
	case "large object":
		return 2613, nil
	case "text search parser":
		return 3601, nil
	case "text search configuration":
		return 3602, nil
	case "default acl":
		return 826, nil
	case "operator of access method":
		return 2602, nil
	case "function of access method":
		return 2603, nil
	case "transform":
		return 3576, nil
	case "parameter ACL":
		return 6243, nil
	case "publication relation":
		return 6106, nil
	case "publication namespace":
		return 6237, nil
	case "role membership", "toast table", "composite type", "index column", "sequence column", "toast table column", "view column", "materialized view column", "composite type column":
		return 0, sqlError("22023", fmt.Sprintf("unsupported object type %q", kind))
	}
	return 0, sqlError("22023", fmt.Sprintf("unrecognized object type %q", kind))
}
func objectInputArray(value sqltype.Value) ([]string, error) {
	out := make([]string, value.ArrayLen())
	for i := range out {
		cell := value.ArrayElement(i)
		if cell.IsNull() {
			return nil, sqlError("22023", "name or argument lists may not contain nulls")
		}
		s, err := cell.Text()
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}
func objectListLength(list string, n int, exact bool) error {
	word := "at least"
	if exact {
		word = "exactly"
	}
	return sqlError("22023", fmt.Sprintf("%s list length must be %s %d", list, word, n))
}
func objectLookupPath(scope *StatementScope, names []string) (namespaceSelection, error) {
	if len(names) == 1 {
		return namespaceSelection{path: scope.deparsePath}, nil
	}
	if len(names) > 3 {
		return namespaceSelection{}, sqlError("42601", "improper qualified name (too many dotted names): "+strings.Join(names, "."))
	}
	if len(names) == 3 {
		if names[0] != scope.op.session.databaseName {
			return namespaceSelection{}, sqlError("0A000", "cross-database references are not implemented: "+strings.Join(names, "."))
		}
		names = names[1:]
	}
	auth := scope.deparseAuth
	if auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: names[0]}) == (ids.ObjectAddress{}) && names[0] != "pg_temp" {
		return namespaceSelection{}, sqlError("3F000", fmt.Sprintf("schema %q does not exist", names[0]))
	}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: names[0]}, {Text: names[1]}}}
	return auth.engine.lookupNamespacesSnapshot(auth, name, functionLookup)
}
func objectLookupDescriptor(scope *StatementScope, class ids.OID, names []string) (catalog.Descriptor, error) {
	path, err := objectLookupPath(scope, names)
	if err != nil {
		return nil, err
	}
	for i := range path.count() {
		a := scope.commentCatalog.NameAddress(ids.NameKey{ClassID: class, Namespace: path.at(i), Name: names[len(names)-1]})
		if d := scope.commentCatalog.Get(a); d != nil {
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
	class, err := objectInputClass(kind)
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	typeName := kind == "type" || kind == "cast" || kind == "domain constraint" || class == 3576
	if (typeName || class == 2613) && values[1].ArrayLen() != 1 {
		return ids.ObjectAddress{}, objectListLength("name", 1, true)
	}
	if class == 2613 && values[1].ArrayElement(0).IsNull() {
		return ids.ObjectAddress{}, sqlError("22023", "large object OID may not be null")
	}
	names, err := objectInputArray(values[1])
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	if len(names) == 0 {
		return ids.ObjectAddress{}, objectListLength("name", 1, false)
	}
	view := scope.commentCatalog
	types := capturedRegtypeInput{auth: scope.deparseAuth, path: scope.deparsePath, database: scope.op.session.databaseName, temporary: temporaryNamespace(scope.deparseAuth), token: scope.op.session.token, op: scope.op}
	parseSyntax := func(input string) (parser.TypeName, error) {
		return parser.ParseTypeName(ctx, input, func(notice sqlstate.Error) { scope.op.emitNotice(levelNotice, notice) })
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
		args, err = objectInputArray(values[2])
		if err != nil {
			return ids.ObjectAddress{}, err
		}
	}
	if (class == 1418 || class == 6237) && len(names) != 1 {
		return ids.ObjectAddress{}, objectListLength("name", 1, true)
	}
	if (kind == "cast" || kind == "domain constraint" || class == 1418 || class == 6237 || class == 6106 || class == 826 || class == 3576) && len(args) != 1 {
		return ids.ObjectAddress{}, objectListLength("argument", 1, true)
	}
	if (class == 2616 || class == 2753) && len(names) < 2 {
		return ids.ObjectAddress{}, objectListLength("name", 2, false)
	}
	if (class == 2602 || class == 2603) && len(names) < 3 {
		return ids.ObjectAddress{}, objectListLength("name", 3, false)
	}
	if (class == 2617 || class == 2602 || class == 2603) && len(args) != 2 {
		return ids.ObjectAddress{}, objectListLength("argument", 2, true)
	}
	switch class {
	case 2612, 2601, 1262, 3466, 3079, 2328, 1417, 6243, 6104, 1260, 2615, 6100, 1213:
		if len(names) != 1 {
			return ids.ObjectAddress{}, objectListLength("name", 1, true)
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
			if role, ok := scope.commentRoles.Lookup(name); ok {
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
			human[i] = regtypeSpelling(view, scope.deparsePath, id)
		}
		p, count := selectExactRoutine(view, path, names[len(names)-1], catalog.ProcedureSignature(signature.String()))
		spelling := make([]string, len(names))
		for i, name := range names {
			spelling[i] = pgIdentifier(name)
		}
		sig := strings.Join(spelling, ".") + "(" + strings.Join(human, ", ") + ")"
		if count == 0 {
			noun := "function"
			if kind == "procedure" {
				noun = "procedure"
			}
			return missing("42883", noun+" "+sig+" does not exist")
		}
		actual := ObjectRoutineKind(p.ID)
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
		for i := range path.count() {
			key := ids.NameKey{ClassID: 2617, Namespace: path.at(i), Name: names[len(names)-1], Signature: fmt.Sprintf("%d,%d", argIDs[0], argIDs[1])}
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
		return missing("42704", fmt.Sprintf("cast from type %s to type %s does not exist", regtypeSpelling(view, scope.deparsePath, ids.OID(typeID)), regtypeSpelling(view, scope.deparsePath, argIDs[0])))
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
		for i := range path.count() {
			if path.at(i).ObjectID != ids.PGCatalogNamespaceOID {
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
			return missing("42704", fmt.Sprintf("constraint %q for domain %s does not exist", args[0], regtypeSpelling(view, scope.deparsePath, ids.OID(typeID))))
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
				actual := objectRelationKind(view, relation)
				if actual != kind {
					return missing("42809", fmt.Sprintf("%q is not a%s %s", relation.Name(), map[string]string{"index": "n"}[kind], kind))
				}
				return relation.Address(), nil
			}
			column := names[len(names)-1]
			for _, sub := range relation.Attributes {
				if objectAttributeName(view, relation, sub) == column {
					return ids.ObjectAddress{1259, relation.Address().ObjectID, sub}, nil
				}
			}
			for sub := int32(-1); sub >= -6; sub-- {
				if objectAttributeName(view, relation, sub) == column {
					return ids.ObjectAddress{1259, relation.Address().ObjectID, sub}, nil
				}
			}
			return missing("42703", fmt.Sprintf("column %q of relation %q does not exist", column, strings.Join(relationNames, ".")))
		}
		name := names[len(names)-1]
		id := ownedAddressByName(relation, class, name)
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
func ownedAddressByName(d *catalog.Relation, class ids.OID, name string) ids.OID {
	switch class {
	case 2606:
		for _, x := range d.Constraints.NotNull {
			if x.Name == name {
				return x.OID
			}
		}
		for _, x := range d.Constraints.Check {
			if x.Name == name {
				return x.OID
			}
		}
		for _, x := range d.Constraints.Unique {
			if x.Name == name {
				return x.OID
			}
		}
		for _, x := range d.Constraints.Exclusion {
			if x.Name == name {
				return x.OID
			}
		}
		for _, x := range d.Constraints.ForeignKeys {
			if x.Name == name {
				return x.OID
			}
		}
	case 2604:
		for i, col := range d.Columns {
			if col.Name == name {
				sub := d.Attributes[i]
				if def, ok := d.ColumnMeta.Defaults[sub]; ok {
					return def.OID
				}
				if def, ok := d.ColumnMeta.Generations[sub]; ok {
					return def.OID
				}
			}
		}
	case 2618:
		if name == "_RETURN" && d.View != nil {
			return d.View.RuleOID()
		}
		for _, x := range rulesOf(d) {
			if x.Name == name {
				return x.OID
			}
		}
	case 3256:
		for _, x := range d.Security.Policies {
			if x.Name == name {
				return x.OID
			}
		}
	}
	return 0
}
