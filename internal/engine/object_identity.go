package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

type objectIdentity struct {
	kind, schema, name, identity, description string
	names, args                               []string
	found                                     bool
}

// These are released address classes, not a whitelist of native providers.
// Recognized classes without owned instances still have correct missing results.
func objectAddressClassKind(class ids.OID) (string, bool) {
	switch class {
	case 1259:
		return "relation", true
	case 1255:
		return "routine", true
	case 1247:
		return "type", true
	case 2615:
		return "schema", true
	case 3456:
		return "collation", true
	case 2607:
		return "conversion", true
	case 3079:
		return "extension", true
	case 3764:
		return "text search template", true
	case 3600:
		return "text search dictionary", true
	case 1262:
		return "database", true
	case 1260:
		return "role", true
	case 2606:
		return "constraint", true
	case 2604:
		return "default value", true
	case 2618:
		return "rule", true
	case 3256:
		return "policy", true
	case 1213:
		return "tablespace", true
	case 2605:
		return "cast", true
	case 2616:
		return "operator class", true
	case 2753:
		return "operator family", true
	case 2617:
		return "operator", true
	case 2612:
		return "language", true
	case 2601:
		return "access method", true
	case 2328:
		return "foreign-data wrapper", true
	case 1417:
		return "server", true
	case 1418:
		return "user mapping", true
	case 2620:
		return "trigger", true
	case 3466:
		return "event trigger", true
	case 6104:
		return "publication", true
	case 6100:
		return "subscription", true
	case 3381:
		return "statistics object", true
	case 2613, 2995:
		return "large object", true
	case 3601:
		return "text search parser", true
	case 3602:
		return "text search configuration", true
	case 826:
		return "default acl", true
	case 1261:
		return "role membership", true
	case 2602:
		return "operator of access method", true
	case 2603:
		return "function of access method", true
	case 3576:
		return "transform", true
	case 6243:
		return "parameter ACL", true
	case 6106:
		return "publication relation", true
	case 6237:
		return "publication namespace", true
	}
	return "", false
}
func objectNamespace(view catalog.Read, namespace ids.ObjectAddress) (actual, canonical string) {
	if ns, ok := view.Namespace(namespace); ok {
		actual, canonical = ns.Name(), ns.Name()
		if ns.Temporary != (ids.SessionToken{}) {
			canonical = "pg_temp"
		}
	}
	return
}
func objectRelationKind(view catalog.Read, d *catalog.Relation) string {
	switch pgcatalog.ClassKind(view, d) {
	case "S":
		return "sequence"
	case "v":
		return "view"
	case "m":
		return "materialized view"
	case "c":
		return "composite type"
	case "i", "I":
		return "index"
	}
	return "table"
}
func objectAttributeName(view catalog.Read, d *catalog.Relation, sub int32) string {
	sub = int32(int16(sub))
	for i, attr := range d.Attributes {
		if attr == sub {
			return d.Columns[i].Name
		}
	}
	if old, ok := d.ColumnMeta.Dropped[sub]; ok {
		return old.Name
	}
	if sub < 0 && sub >= -6 && pgcatalog.HasSystemAttributes(view, d) {
		return [...]string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"}[-sub-1]
	}
	return ""
}
func objectQualifiedType(view catalog.Read, id ids.OID) string {
	return regtypeSpellingMode(view, nil, id, true)
}

func identifyAddress(view catalog.Read, roles authorization.Read, path []ids.ObjectAddress, a ids.ObjectAddress) (objectIdentity, error) {
	kind, known := objectAddressClassKind(a.ClassID)
	if !known {
		return objectIdentity{}, sqlError("XX000", fmt.Sprintf("unsupported object class: %d", a.ClassID))
	}
	r := objectIdentity{kind: kind}
	key := a
	key.SubID = 0
	d := view.Get(key)
	if a.ClassID == ids.ProcedureClassID {
		p, ok := routineIdentityByOID(view, a.ObjectID)
		if !ok {
			return r, nil
		}
		r.found = true
		r.kind = routine.ObjectRoutineKind(p.ID)
		ns, _ := objectNamespace(view, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: p.Namespace})
		r.schema = catalog.Identifier(ns)
		r.names = []string{ns, p.Name}
		if p.Signature != "" {
			for arg := range strings.SplitSeq(string(p.Signature), ",") {
				id, _ := strconv.ParseUint(arg, 10, 32)
				r.args = append(r.args, objectQualifiedType(view, ids.OID(id)))
			}
		}
		r.identity = catalog.Identifier(ns) + "." + catalog.Identifier(p.Name) + "(" + strings.Join(r.args, ",") + ")"
		r.description = "function " + routineSpelling(view, path, p.ID, true, false)
		return r, nil
	}
	if a.ClassID == 1260 {
		role, ok := roles.Find(view.Owner().Principals.Principal(uint64(a.ObjectID)))
		if !ok {
			return r, nil
		}
		r.found = true
		r.name = catalog.Identifier(role.Name)
		r.identity = r.name
		r.names = []string{role.Name}
		r.description = "role " + r.name
		return r, nil
	}
	if a.ClassID == 1213 {
		name := ""
		if a.ObjectID == 1663 {
			name = "pg_default"
		}
		if a.ObjectID == 1664 {
			name = "pg_global"
		}
		if name == "" {
			return r, nil
		}
		r.found = true
		r.name = name
		r.identity = name
		r.names = []string{name}
		r.description = "tablespace " + name
		return r, nil
	}
	if a.ClassID == 2605 {
		for i := range sqltype.CastMetadataCount() {
			fact, _ := sqltype.CastMetadataAt(i)
			if ids.OID(fact.OID) != a.ObjectID {
				continue
			}
			source, target := objectQualifiedType(view, ids.OID(fact.Source)), objectQualifiedType(view, ids.OID(fact.Target))
			r.found = true
			r.names = []string{source}
			r.args = []string{target}
			r.identity = "(" + source + " AS " + target + ")"
			r.description = "cast from " + regtypeSpelling(view, path, ids.OID(fact.Source)) + " to " + regtypeSpelling(view, path, ids.OID(fact.Target))
			break
		}
		return r, nil
	}
	if a.ClassID == 2616 || a.ClassID == 2753 {
		var name string
		var method uint32
		if a.ClassID == 2616 {
			for i := range sqltype.IndexClassMetadataCount() {
				fact, _ := sqltype.IndexClassMetadataAt(i)
				if ids.OID(fact.OID) == a.ObjectID {
					name, method = fact.Name, fact.Method
					break
				}
			}
		} else if fact, ok := sqltype.IndexFamilyMetadataByOID(uint32(a.ObjectID)); ok {
			name, method = fact.Name, fact.Method
		}
		if name == "" {
			return r, nil
		}
		am := "btree"
		if method == 405 {
			am = "hash"
		}
		r.found = true
		r.schema = "pg_catalog"
		r.name = catalog.Identifier(name)
		r.names = []string{am, "pg_catalog", name}
		r.identity = "pg_catalog." + r.name + " USING " + am
		r.description = kind + " " + r.name + " for access method " + am
		return r, nil
	}
	if a.ClassID == 2606 || a.ClassID == 2604 || a.ClassID == 2618 || a.ClassID == 3256 {
		return identifyOwnedAddress(view, path, a, r), nil
	}
	if d == nil {
		return r, nil
	}
	r.found = true
	actual, canonical := objectNamespace(view, d.Namespace())
	if actual != "" {
		r.schema = catalog.Identifier(actual)
	}
	r.name = catalog.Identifier(d.Name())
	r.identity = r.name
	r.names = []string{d.Name()}
	if actual != "" {
		r.identity = catalog.Identifier(canonical) + "." + r.name
		r.names = []string{canonical, d.Name()}
	}
	display := r.identity
	if actual == "" || namedObjectVisible(view, path, d) {
		display = r.name
	}
	switch d := d.(type) {
	case *catalog.Namespace:
		_, canonical = objectNamespace(view, d.Address())
		r.identity = catalog.Identifier(canonical)
		r.names = []string{canonical}
		display = r.name
	case *catalog.Relation:
		r.kind = objectRelationKind(view, d)
		display = regclassSpelling(view, path, d.Address().ObjectID)
		if a.SubID != 0 {
			r.kind += " column"
			attr := objectAttributeName(view, d, a.SubID)
			if attr == "" {
				return objectIdentity{kind: r.kind}, nil
			}
			r.identity += "." + catalog.Identifier(attr)
			r.names = append(r.names, attr)
			r.description = "column " + attr + " of " + objectRelationKind(view, d) + " " + display
			return r, nil
		}
	case *catalog.Type:
		r.identity = objectQualifiedType(view, a.ObjectID)
		r.names = []string{r.identity}
		display = regtypeSpelling(view, path, a.ObjectID)
	case *catalog.Operator:
		r.name = ""
		left, right := "NONE", "NONE"
		if d.Left != 0 {
			left = objectQualifiedType(view, d.Left)
			r.args = append(r.args, left)
		}
		if d.Right != 0 {
			right = objectQualifiedType(view, d.Right)
			r.args = append(r.args, right)
		}
		r.identity = catalog.Identifier(canonical) + "." + d.Name() + "(" + left + "," + right + ")"
		display = d.Name()
		if !namedObjectVisible(view, path, d) {
			display = catalog.Identifier(canonical) + "." + display
		}
		humanLeft, humanRight := "NONE", "NONE"
		if d.Left != 0 {
			humanLeft = regtypeSpelling(view, path, d.Left)
		}
		if d.Right != 0 {
			humanRight = regtypeSpelling(view, path, d.Right)
		}
		display += "(" + humanLeft + "," + humanRight + ")"
	}
	r.description = r.kind + " " + display
	return r, nil
}

// Owned definitions retain their actual owning relation/type OIDs. They are not
// descriptors and never get an invented namespace or simple-name field.
func identifyOwnedAddress(view catalog.Read, path []ids.ObjectAddress, a ids.ObjectAddress, r objectIdentity) objectIdentity {
	for _, descriptor := range view.Descriptors() {
		if domain, ok := descriptor.(*catalog.Type); ok && a.ClassID == 2606 {
			for _, check := range domain.DomainChecks {
				if check.OID != a.ObjectID {
					continue
				}
				r.found = true
				r.kind = "domain constraint"
				ns, _ := objectNamespace(view, domain.Namespace())
				r.schema = catalog.Identifier(ns)
				typeName := objectQualifiedType(view, ids.OID(domain.SQLType.ID()))
				r.identity = catalog.Identifier(check.Name) + " on " + typeName
				r.names = []string{typeName}
				r.args = []string{check.Name}
				r.description = "constraint " + check.Name
				return r
			}
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		name, sub := ownedAddressName(d, a)
		if name == "" {
			continue
		}
		r.found = true
		actual, canonical := objectNamespace(view, d.Namespace())
		relation := catalog.Identifier(canonical) + "." + catalog.Identifier(d.Name())
		r.names = []string{canonical, d.Name(), name}
		description := objectRelationKind(view, d) + " " + regclassSpelling(view, path, d.Address().ObjectID)
		switch a.ClassID {
		case 2606:
			r.kind = "table constraint"
			r.schema = catalog.Identifier(actual)
			r.identity = catalog.Identifier(name) + " on " + relation
			r.description = "constraint " + name + " on " + description
		case 2604:
			attr := objectAttributeName(view, d, sub)
			r.names[2] = attr
			r.identity = "for " + relation + "." + catalog.Identifier(attr)
			r.description = "default value for column " + attr + " of " + description
		case 2618:
			r.identity = catalog.Identifier(name) + " on " + relation
			r.description = "rule " + name + " on " + description
		case 3256:
			r.identity = catalog.Identifier(name) + " on " + relation
			r.description = "policy " + name + " on " + description
		}
		return r
	}
	return r
}
func ownedAddressName(d *catalog.Relation, a ids.ObjectAddress) (string, int32) {
	switch a.ClassID {
	case 2606:
		for _, x := range d.Constraints.NotNull {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
		for _, x := range d.Constraints.Check {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
		for _, x := range d.Constraints.Unique {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
		for _, x := range d.Constraints.Exclusion {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
		for _, x := range d.Constraints.ForeignKeys {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
	case 2604:
		for sub, x := range d.ColumnMeta.Defaults {
			if x.OID == a.ObjectID {
				return "default", sub
			}
		}
		for sub, x := range d.ColumnMeta.Generations {
			if x.OID == a.ObjectID {
				return "generation", sub
			}
		}
	case 2618:
		if d.View != nil && d.View.RuleOID() == a.ObjectID {
			return "_RETURN", 0
		}
		for _, x := range rulesOf(d) {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
	case 3256:
		for _, x := range d.Security.Policies {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
	}
	return "", 0
}

func objectAddressTextArray(d *sqltype.Descriptor, names []string) (sqltype.Value, error) {
	values := make([]sqltype.Value, len(names))
	for i, name := range names {
		values[i] = sqltype.String(d.Element(), name)
	}
	var dims []sqltype.ArrayDimension
	if len(names) != 0 {
		dims = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(names))}}
	}
	return sqltype.OwnedArrayValue(d, dims, values)
}

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
