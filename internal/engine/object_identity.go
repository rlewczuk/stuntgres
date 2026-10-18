package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
func objectNamespace(view CatalogRead, namespace ids.ObjectAddress) (actual, canonical string) {
	if ns, ok := view.Namespace(namespace); ok {
		actual, canonical = ns.Name(), ns.Name()
		if ns.Temporary != (ids.SessionToken{}) {
			canonical = "pg_temp"
		}
	}
	return
}
func objectRelationKind(view CatalogRead, d *catalog.Relation) string {
	switch pgClassKind(view, d) {
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
func objectAttributeName(view CatalogRead, d *catalog.Relation, sub int32) string {
	sub = int32(int16(sub))
	for i, attr := range d.Attributes {
		if attr == sub {
			return d.Columns[i].Name
		}
	}
	if old, ok := d.DroppedAttributes[sub]; ok {
		return old.Name
	}
	if sub < 0 && sub >= -6 && hasSystemAttributes(view, d) {
		return [...]string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"}[-sub-1]
	}
	return ""
}
func objectQualifiedType(view CatalogRead, id ids.OID) string {
	return regtypeSpellingMode(view, nil, id, true)
}

func identifyAddress(view CatalogRead, roles authorization.Read, path []ids.ObjectAddress, a ids.ObjectAddress) (objectIdentity, error) {
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
		r.kind = objectRoutineKind(p.ID)
		ns, _ := objectNamespace(view, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: p.Namespace})
		r.schema = pgIdentifier(ns)
		r.names = []string{ns, p.Name}
		if p.Signature != "" {
			for arg := range strings.SplitSeq(string(p.Signature), ",") {
				id, _ := strconv.ParseUint(arg, 10, 32)
				r.args = append(r.args, objectQualifiedType(view, ids.OID(id)))
			}
		}
		r.identity = pgIdentifier(ns) + "." + pgIdentifier(p.Name) + "(" + strings.Join(r.args, ",") + ")"
		r.description = "function " + routineSpelling(view, path, p.ID, true, false)
		return r, nil
	}
	if a.ClassID == 1260 {
		role, ok := roles.Find(view.Owner().Principals.Principal(uint64(a.ObjectID)))
		if !ok {
			return r, nil
		}
		r.found = true
		r.name = pgIdentifier(role.Name)
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
		r.name = pgIdentifier(name)
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
		r.schema = pgIdentifier(actual)
	}
	r.name = pgIdentifier(d.Name())
	r.identity = r.name
	r.names = []string{d.Name()}
	if actual != "" {
		r.identity = pgIdentifier(canonical) + "." + r.name
		r.names = []string{canonical, d.Name()}
	}
	display := r.identity
	if actual == "" || namedObjectVisible(view, path, d) {
		display = r.name
	}
	switch d := d.(type) {
	case *namespaceDescriptor:
		_, canonical = objectNamespace(view, d.Address())
		r.identity = pgIdentifier(canonical)
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
			r.identity += "." + pgIdentifier(attr)
			r.names = append(r.names, attr)
			r.description = "column " + attr + " of " + objectRelationKind(view, d) + " " + display
			return r, nil
		}
	case *typeDescriptor:
		r.identity = objectQualifiedType(view, a.ObjectID)
		r.names = []string{r.identity}
		display = regtypeSpelling(view, path, a.ObjectID)
	case *operatorDescriptor:
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
		r.identity = pgIdentifier(canonical) + "." + d.Name() + "(" + left + "," + right + ")"
		display = d.Name()
		if !namedObjectVisible(view, path, d) {
			display = pgIdentifier(canonical) + "." + display
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
func identifyOwnedAddress(view CatalogRead, path []ids.ObjectAddress, a ids.ObjectAddress, r objectIdentity) objectIdentity {
	for _, descriptor := range view.Descriptors() {
		if domain, ok := descriptor.(*typeDescriptor); ok && a.ClassID == 2606 {
			for _, check := range domain.DomainChecks {
				if check.OID != a.ObjectID {
					continue
				}
				r.found = true
				r.kind = "domain constraint"
				ns, _ := objectNamespace(view, domain.Namespace())
				r.schema = pgIdentifier(ns)
				typeName := objectQualifiedType(view, ids.OID(domain.SQLType.ID()))
				r.identity = pgIdentifier(check.Name) + " on " + typeName
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
		relation := pgIdentifier(canonical) + "." + pgIdentifier(d.Name())
		r.names = []string{canonical, d.Name(), name}
		description := objectRelationKind(view, d) + " " + regclassSpelling(view, path, d.Address().ObjectID)
		switch a.ClassID {
		case 2606:
			r.kind = "table constraint"
			r.schema = pgIdentifier(actual)
			r.identity = pgIdentifier(name) + " on " + relation
			r.description = "constraint " + name + " on " + description
		case 2604:
			attr := objectAttributeName(view, d, sub)
			r.names[2] = attr
			r.identity = "for " + relation + "." + pgIdentifier(attr)
			r.description = "default value for column " + attr + " of " + description
		case 2618:
			r.identity = pgIdentifier(name) + " on " + relation
			r.description = "rule " + name + " on " + description
		case 3256:
			r.identity = pgIdentifier(name) + " on " + relation
			r.description = "policy " + name + " on " + description
		}
		return r
	}
	return r
}
func ownedAddressName(d *catalog.Relation, a ids.ObjectAddress) (string, int32) {
	switch a.ClassID {
	case 2606:
		for _, x := range d.NotNullConstraints {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
		for _, x := range d.CheckConstraints {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
		for _, x := range d.UniqueConstraints {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
		for _, x := range d.ExclusionConstraints {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
		for _, x := range d.ForeignKeys {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
	case 2604:
		for sub, x := range d.Defaults {
			if x.OID == a.ObjectID {
				return "default", sub
			}
		}
		for sub, x := range d.Generations {
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
		for _, x := range d.Policies {
			if x.OID == a.ObjectID {
				return x.Name, 0
			}
		}
	}
	return "", 0
}
