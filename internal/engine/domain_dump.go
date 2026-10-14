package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

type dumpDomain struct{ owner, sql string }

// Replay names are transient rendering predictions keyed by captured identity,
// not a catalog or destination OID allocator. Only domain/enum creators run here.
type domainReplayNames struct {
	occupied map[NameKey]sqltype.ID
	names    map[sqltype.ID]NameKey
	arrays   map[sqltype.ID]sqltype.ID
	elements map[sqltype.ID]sqltype.ID
}

func newDomainReplayNames() *domainReplayNames {
	r := &domainReplayNames{
		occupied: make(map[NameKey]sqltype.ID), names: make(map[sqltype.ID]NameKey),
		arrays: make(map[sqltype.ID]sqltype.ID), elements: make(map[sqltype.ID]sqltype.ID),
	}
	for _, typ := range sqltype.Builtins() {
		namespace := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
		if typ.NamespaceName() == "information_schema" {
			namespace.ObjectID = 13229
		}
		key := NameKey{ClassID: TypeClassID, Namespace: namespace, Name: typ.Name()}
		r.occupied[key], r.names[typ.ID()] = typ.ID(), key
		r.arrays[typ.ID()] = typ.AssociatedArray()
		if typ.IsArray() {
			r.elements[typ.ID()] = typ.Element().ID()
		}
	}
	return r
}

func (r *domainReplayNames) arrayName(key NameKey) NameKey {
	key.Name = chooseAssociatedArrayName(key.Name, func(candidate string) bool {
		key.Name = candidate
		return r.occupied[key] != 0
	})
	return key
}

func (r *domainReplayNames) reserve(d *typeDescriptor) error {
	key := descriptorNameKey(d)
	if incumbent := r.occupied[key]; incumbent != 0 {
		element := r.elements[incumbent]
		if element == 0 || r.arrays[element] != incumbent {
			return fmt.Errorf("stuntgres: conflicting domain replay type name")
		}
		moved := r.arrayName(key)
		delete(r.occupied, key)
		r.occupied[moved], r.names[incumbent] = incumbent, moved
	}
	r.occupied[key], r.names[d.typ.ID()] = d.typ.ID(), key
	return nil
}

func (r *domainReplayNames) associate(d *typeDescriptor) {
	if d.array == 0 {
		return
	}
	key := r.arrayName(r.names[d.typ.ID()])
	r.occupied[key], r.names[d.array] = d.array, key
	r.arrays[d.typ.ID()], r.elements[d.array] = d.array, d.typ.ID()
}

func defaultDomainACL(d *typeDescriptor) bool {
	if !d.acl.Explicit {
		return false
	}
	var public, owner, options authorization.Privileges
	for _, entry := range d.acl.Entries {
		if entry.Grantor != d.owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (authorization.PrincipalRef{}) || entry.GrantOptions != 0 {
				return false
			}
			public |= entry.Privileges
		} else {
			if entry.Grantee.Role != d.owner {
				return false
			}
			owner |= entry.Privileges
			options |= entry.GrantOptions
		}
	}
	return public == authorization.UsagePrivilege && owner == authorization.UsagePrivilege && options == 0
}

// Catalog rendering resolves the captured namespace, including renamed schemas;
// the immutable codec never consults a mutable engine to format a domain name.
func catalogTypeDeclaration(view CatalogRead, u sqltype.Usage) (string, error) {
	if u.Type.IsArray() {
		array, ok := view.LookupArray(u.Type.Element())
		if !ok || array != u.Type || !u.Valid() {
			return "", fmt.Errorf("stuntgres: invalid array declaration authority")
		}
		element := u
		element.Type = u.Type.Element()
		declaration, err := catalogTypeDeclaration(view, element)
		if err != nil {
			return "", err
		}
		return declaration + "[]", nil
	}
	if !u.Type.IsUserDefined() && !(u.Type.IsDomain() && u.Type.Concrete()) {
		return u.Declaration()
	}
	descriptor, ok := view.Descriptor(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(u.Type.ID())})
	if !ok || descriptor.(*typeDescriptor).typ != u.Type || !u.Valid() {
		return "", fmt.Errorf("stuntgres: invalid domain declaration authority")
	}
	namespace, ok := view.Descriptor(descriptor.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing domain namespace")
	}
	return quoteIdentifier(namespace.Name()) + "." + quoteIdentifier(descriptor.Name()), nil
}

func domainDumpStatement(ctx context.Context, view CatalogRead, roles authorization.Read, d *typeDescriptor, replayNames *domainReplayNames) (dumpDomain, error) {
	if err := ctx.Err(); err != nil {
		return dumpDomain{}, err
	}
	if !defaultDomainACL(d) {
		return dumpDomain{}, fmt.Errorf("stuntgres: dump cannot serialize changed domain ACL")
	}
	qualified, err := catalogTypeDeclaration(view, d.typ.Usage())
	if err != nil {
		return dumpDomain{}, err
	}
	owner, err := roles.Descriptor(d.owner)
	if err != nil {
		return dumpDomain{}, err
	}
	if d.typ.IsEnum() {
		labels := d.typ.EnumLabels()
		for i, label := range labels {
			labels[i] = "'" + strings.ReplaceAll(label, "'", "''") + "'"
		}
		return dumpDomain{owner.Name, "CREATE TYPE " + qualified + " AS ENUM (" + strings.Join(labels, ", ") + ");\n"}, nil
	}
	base := d.typ.DomainBase()
	declaration, err := catalogTypeDeclaration(view, base)
	if err != nil {
		return dumpDomain{}, err
	}
	if base.Type.IsArray() {
		if d.dimensions == 0 {
			key, exists := replayNames.names[base.Type.ID()]
			if !exists || replayNames.arrays[base.Type.Element().ID()] != base.Type.ID() {
				return dumpDomain{}, fmt.Errorf("stuntgres: missing domain replay array identity")
			}
			namespace, exists := view.Descriptor(key.Namespace)
			if !exists {
				return dumpDomain{}, fmt.Errorf("stuntgres: missing domain replay array namespace")
			}
			declaration = quoteIdentifier(namespace.Name()) + "." + quoteIdentifier(key.Name)
		} else {
			declaration = strings.TrimSuffix(declaration, "[]") + strings.Repeat("[]", int(d.dimensions))
		}
	}
	collation := ""
	if base.Collation != base.Type.Usage().Collation {
		name := collationSQLName(view, base.Collation.ID)
		if name == "" {
			return dumpDomain{}, fmt.Errorf("stuntgres: unsupported domain collation")
		}
		collation = " COLLATE " + name
	}
	return dumpDomain{owner.Name, "CREATE DOMAIN " + qualified + " AS " + declaration + collation + ";\n"}, nil
}

func catalogLiteralSQL(ctx context.Context, view CatalogRead, n literalExpression) (string, error) {
	return catalogLiteralWithACL(ctx, view, n, aclItemContextFormatter(ctx))
}

func catalogLiteralWithACL(ctx context.Context, view CatalogRead, n literalExpression, aclOutput sqltype.ACLItemFormatter) (string, error) {
	u := n.ResultUsage()
	if n.value.Type() != u.Type {
		return "", fmt.Errorf("stuntgres: invalid stored literal")
	}
	if u.Type.IsEnum() {
		declaration, err := catalogTypeDeclaration(view, u)
		if err != nil {
			return "", err
		}
		literal, err := u.Type.SQLLiteral(ctx, n.value)
		if err != nil {
			return "", err
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
	}
	if u.Type.IsDomain() {
		declaration, err := catalogTypeDeclaration(view, u)
		if err != nil {
			return "", err
		}
		value, err := n.value.BaseValue()
		if err != nil {
			return "", err
		}
		n.usage = u.BaseUsage()
		n.value = value
		literal, err := catalogLiteralWithACL(ctx, view, n, aclOutput)
		if err != nil {
			return "", err
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
	}
	if sqltype.IsRoutineAlias(u.Type.ID()) && !n.value.IsNull() {
		return routineLiteralSQL(view, n.value)
	}
	if u.Type.IsArray() || u.Type.IsComposite() {
		declaration, err := catalogTypeDeclaration(view, u)
		if err != nil {
			return "", err
		}
		if u.Type.ContainsRegproc() && !n.value.IsNull() {
			return routineContainerLiteral(ctx, view, n.value, u, aclOutput)
		}
		literal := "NULL"
		if !n.value.IsNull() {
			text, err := u.Type.OutputWithEnvironment(ctx, n.value, sqltype.OutputEnvironment{Regclass: catalogRegclassOutput{view: view}, Regtype: dumpRegtypeOutput{view: view}, Regrole: regroleContextFormatter(ctx), Operator: dumpOperatorOutput{}, Regproc: catalogRoutineOutput{view: view, qualified: true}, ACLItem: aclOutput})
			if err != nil {
				return "", err
			}
			literal = "'" + strings.ReplaceAll(text, "'", "''") + "'"
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
	}
	if u.Type.ID() == sqltype.ACLItem && !n.value.IsNull() {
		text, err := u.Type.OutputWithEnvironment(ctx, n.value, sqltype.OutputEnvironment{ACLItem: aclOutput})
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS ACLITEM)", nil
	}
	if u.Type.ID() == sqltype.Regrole && !n.value.IsNull() {
		text, err := u.Type.OutputWithEnvironment(ctx, n.value, sqltype.OutputEnvironment{Regrole: regroleContextFormatter(ctx)})
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS REGROLE)", nil
	}
	if u.Type.ID() == sqltype.Regtype && !n.value.IsNull() {
		id, err := n.value.TypeOID()
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(regtypeReference(view, id), "'", "''") + "' AS REGTYPE)", nil
	}
	if u.Type.ID() == sqltype.Regclass && !n.value.IsNull() {
		text, err := n.value.Text()
		if err != nil {
			return "", err
		}
		id, err := strconv.ParseUint(text, 10, 32)
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(regclassSpelling(view, nil, OID(id)), "'", "''") + "' AS REGCLASS)", nil
	}
	return literalSQL(ctx, n)
}

type catalogRegclassOutput struct {
	view CatalogRead
	path []ObjectAddress
}

func (r catalogRegclassOutput) FormatRegclass(id uint32) (string, error) {
	return regclassSpelling(r.view, r.path, OID(id)), nil
}
