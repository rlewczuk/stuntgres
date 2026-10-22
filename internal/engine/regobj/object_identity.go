package regobj

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

type ObjectIdentity struct {
	Kind, Schema, Name, Identity, Description string
	Names, Args                               []string
	Found                                     bool
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
func ObjectRelationKind(view catalog.Read, d *catalog.Relation) string {
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
func ObjectAttributeName(view catalog.Read, d *catalog.Relation, sub int32) string {
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

func IdentifyAddress(view catalog.Read, roles authorization.Read, path []ids.ObjectAddress, a ids.ObjectAddress) (ObjectIdentity, error) {
	kind, known := objectAddressClassKind(a.ClassID)
	if !known {
		return ObjectIdentity{}, sqlError("XX000", fmt.Sprintf("unsupported object class: %d", a.ClassID))
	}
	r := ObjectIdentity{Kind: kind}
	key := a
	key.SubID = 0
	d := view.Get(key)
	if a.ClassID == ids.ProcedureClassID {
		p, ok := RoutineIdentityByOID(view, a.ObjectID)
		if !ok {
			return r, nil
		}
		r.Found = true
		r.Kind = routine.ObjectRoutineKind(p.ID)
		ns, _ := objectNamespace(view, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: p.Namespace})
		r.Schema = catalog.Identifier(ns)
		r.Names = []string{ns, p.Name}
		if p.Signature != "" {
			for arg := range strings.SplitSeq(string(p.Signature), ",") {
				id, _ := strconv.ParseUint(arg, 10, 32)
				r.Args = append(r.Args, objectQualifiedType(view, ids.OID(id)))
			}
		}
		r.Identity = catalog.Identifier(ns) + "." + catalog.Identifier(p.Name) + "(" + strings.Join(r.Args, ",") + ")"
		r.Description = "function " + RoutineSpelling(view, path, p.ID, true, false)
		return r, nil
	}
	if a.ClassID == 1260 {
		role, ok := roles.Find(view.Owner().Principals.Principal(uint64(a.ObjectID)))
		if !ok {
			return r, nil
		}
		r.Found = true
		r.Name = catalog.Identifier(role.Name)
		r.Identity = r.Name
		r.Names = []string{role.Name}
		r.Description = "role " + r.Name
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
		r.Found = true
		r.Name = name
		r.Identity = name
		r.Names = []string{name}
		r.Description = "tablespace " + name
		return r, nil
	}
	if a.ClassID == 2605 {
		for i := range sqltype.CastMetadataCount() {
			fact, _ := sqltype.CastMetadataAt(i)
			if ids.OID(fact.OID) != a.ObjectID {
				continue
			}
			source, target := objectQualifiedType(view, ids.OID(fact.Source)), objectQualifiedType(view, ids.OID(fact.Target))
			r.Found = true
			r.Names = []string{source}
			r.Args = []string{target}
			r.Identity = "(" + source + " AS " + target + ")"
			r.Description = "cast from " + TypeSpelling(view, path, ids.OID(fact.Source)) + " to " + TypeSpelling(view, path, ids.OID(fact.Target))
			break
		}
		return r, nil
	}
	if a.ClassID == 2606 || a.ClassID == 2604 || a.ClassID == 2618 || a.ClassID == 3256 {
		return identifyOwnedAddress(view, path, a, r), nil
	}
	if d == nil {
		return r, nil
	}
	r.Found = true
	if member, ok := identifyOperatorFamilyMember(view, path, d, r); ok {
		return member, nil
	}
	actual, canonical := objectNamespace(view, d.Namespace())
	if actual != "" {
		r.Schema = catalog.Identifier(actual)
	}
	r.Name = catalog.Identifier(d.Name())
	r.Identity = r.Name
	r.Names = []string{d.Name()}
	if actual != "" {
		r.Identity = catalog.Identifier(canonical) + "." + r.Name
		r.Names = []string{canonical, d.Name()}
	}
	display := r.Identity
	if actual == "" || NamedObjectVisible(view, path, d) {
		display = r.Name
	}
	switch d := d.(type) {
	case *catalog.OperatorClass:
		method, ok := catalog.AccessMethodByOID(d.Method)
		if !ok {
			return r, fmt.Errorf("stuntgres: missing operator class access method")
		}
		r.Names = []string{method.Name, canonical, d.Name()}
		r.Identity = catalog.Identifier(canonical) + "." + r.Name + " USING " + method.Name
		r.Description = "operator class " + display + " for access method " + method.Name
		return r, nil
	case *catalog.OperatorFamily:
		method, ok := catalog.AccessMethodByOID(d.Method)
		if !ok {
			return r, fmt.Errorf("stuntgres: missing operator family access method")
		}
		am := method.Name
		r.Names = []string{am, canonical, d.Name()}
		r.Identity = catalog.Identifier(canonical) + "." + r.Name + " USING " + am
		r.Description = "operator family " + display + " for access method " + am
		return r, nil
	case *catalog.Namespace:
		_, canonical = objectNamespace(view, d.Address())
		r.Identity = catalog.Identifier(canonical)
		r.Names = []string{canonical}
		display = r.Name
	case *catalog.Relation:
		r.Kind = ObjectRelationKind(view, d)
		display = ClassSpelling(view, path, d.Address().ObjectID)
		if a.SubID != 0 {
			r.Kind += " column"
			attr := ObjectAttributeName(view, d, a.SubID)
			if attr == "" {
				return ObjectIdentity{Kind: r.Kind}, nil
			}
			r.Identity += "." + catalog.Identifier(attr)
			r.Names = append(r.Names, attr)
			r.Description = "column " + attr + " of " + ObjectRelationKind(view, d) + " " + display
			return r, nil
		}
	case *catalog.Type:
		r.Identity = objectQualifiedType(view, a.ObjectID)
		r.Names = []string{r.Identity}
		display = TypeSpelling(view, path, a.ObjectID)
	case *catalog.Operator:
		r.Name = ""
		left, right := "NONE", "NONE"
		if d.Left != 0 {
			left = objectQualifiedType(view, d.Left)
			r.Args = append(r.Args, left)
		}
		if d.Right != 0 {
			right = objectQualifiedType(view, d.Right)
			r.Args = append(r.Args, right)
		}
		r.Identity = catalog.Identifier(canonical) + "." + d.Name() + "(" + left + "," + right + ")"
		display = d.Name()
		if !NamedObjectVisible(view, path, d) {
			display = catalog.Identifier(canonical) + "." + display
		}
		humanLeft, humanRight := "NONE", "NONE"
		if d.Left != 0 {
			humanLeft = TypeSpelling(view, path, d.Left)
		}
		if d.Right != 0 {
			humanRight = TypeSpelling(view, path, d.Right)
		}
		display += "(" + humanLeft + "," + humanRight + ")"
	}
	r.Description = r.Kind + " " + display
	return r, nil
}

// Owned definitions retain their actual owning relation/type OIDs. They are not
// descriptors and never get an invented namespace or simple-name field.
func identifyOwnedAddress(view catalog.Read, path []ids.ObjectAddress, a ids.ObjectAddress, r ObjectIdentity) ObjectIdentity {
	for _, descriptor := range view.Descriptors() {
		if domain, ok := descriptor.(*catalog.Type); ok && a.ClassID == 2606 {
			if nn := domain.DomainNotNull; nn != nil && nn.OID == a.ObjectID {
				r.Found, r.Kind = true, "domain constraint"
				ns, _ := objectNamespace(view, domain.Namespace())
				r.Schema = catalog.Identifier(ns)
				typeName := objectQualifiedType(view, ids.OID(domain.SQLType.ID()))
				r.Identity = catalog.Identifier(nn.Name) + " on " + typeName
				r.Names, r.Args = []string{typeName}, []string{nn.Name}
				r.Description = "constraint " + nn.Name
				return r
			}
			for _, check := range domain.DomainChecks {
				if check.OID != a.ObjectID {
					continue
				}
				r.Found = true
				r.Kind = "domain constraint"
				ns, _ := objectNamespace(view, domain.Namespace())
				r.Schema = catalog.Identifier(ns)
				typeName := objectQualifiedType(view, ids.OID(domain.SQLType.ID()))
				r.Identity = catalog.Identifier(check.Name) + " on " + typeName
				r.Names = []string{typeName}
				r.Args = []string{check.Name}
				r.Description = "constraint " + check.Name
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
		r.Found = true
		actual, canonical := objectNamespace(view, d.Namespace())
		relation := catalog.Identifier(canonical) + "." + catalog.Identifier(d.Name())
		r.Names = []string{canonical, d.Name(), name}
		description := ObjectRelationKind(view, d) + " " + ClassSpelling(view, path, d.Address().ObjectID)
		switch a.ClassID {
		case 2606:
			r.Kind = "table constraint"
			r.Schema = catalog.Identifier(actual)
			r.Identity = catalog.Identifier(name) + " on " + relation
			r.Description = "constraint " + name + " on " + description
		case 2604:
			attr := ObjectAttributeName(view, d, sub)
			r.Names[2] = attr
			r.Identity = "for " + relation + "." + catalog.Identifier(attr)
			r.Description = "default value for column " + attr + " of " + description
		case 2618:
			r.Identity = catalog.Identifier(name) + " on " + relation
			r.Description = "rule " + name + " on " + description
		case 3256:
			r.Identity = catalog.Identifier(name) + " on " + relation
			r.Description = "policy " + name + " on " + description
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
		name, found := "", false
		_ = d.VisitRules(func(rule catalog.RuleFact) error {
			if !found && rule.OID == a.ObjectID {
				name, found = rule.Name, true
			}
			return nil
		})
		if found {
			return name, 0
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

func ObjectAddressTextArray(d *sqltype.Descriptor, names []string) (sqltype.Value, error) {
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

func ObjectInputClass(kind string) (ids.OID, error) {
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

func ObjectInputArray(value sqltype.Value) ([]string, error) {
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

func ObjectListLength(list string, n int, exact bool) error {
	word := "at least"
	if exact {
		word = "exactly"
	}
	return sqlError("22023", fmt.Sprintf("%s list length must be %s %d", list, word, n))
}

func OwnedAddressByName(d *catalog.Relation, class ids.OID, name string) ids.OID {
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
		var oid ids.OID
		found := false
		_ = d.VisitRules(func(rule catalog.RuleFact) error {
			if !found && rule.Name == name {
				oid, found = rule.OID, true
			}
			return nil
		})
		if found {
			return oid
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
