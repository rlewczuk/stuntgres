package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/sqltype"
)

type dumpDomain struct{ owner, sql string }

func defaultDomainACL(d *typeDescriptor) bool {
	if !d.acl.Explicit {
		return false
	}
	var public, owner, options Privileges
	for _, entry := range d.acl.Entries {
		if entry.Grantor != d.owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (PrincipalRef{}) || entry.GrantOptions != 0 {
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
	return public == UsagePrivilege && owner == UsagePrivilege && options == UsagePrivilege
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
	if !u.Type.IsDomain() {
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

func domainDumpStatements(ctx context.Context, view CatalogRead, roles RoleRead, domains []*typeDescriptor) ([]dumpDomain, error) {
	dependencies := make(map[OID][]OID, len(domains))
	descriptors := make(map[OID]*typeDescriptor, len(domains))
	for _, d := range domains {
		if !defaultDomainACL(d) {
			return nil, fmt.Errorf("stuntgres: dump cannot serialize changed domain ACL")
		}
		id := d.Address().ObjectID
		descriptors[id] = d
		dependencies[id] = nil
		base := d.typ.DomainBase().Type
		if base.IsArray() {
			base = base.Element()
		}
		if base.IsDomain() {
			dependencies[id] = []OID{OID(base.ID())}
		}
	}
	order, err := viewDumpOrder(ctx, dependencies)
	if err != nil {
		return nil, err
	}
	result := make([]dumpDomain, 0, len(order))
	for _, id := range order {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		d := descriptors[id]
		base := d.typ.DomainBase()
		declaration, er := catalogTypeDeclaration(view, base)
		if er != nil {
			return nil, er
		}
		if base.Type.IsArray() {
			if d.dimensions == 0 {
				array := view.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(base.Type.ID())}]
				declaration = quoteIdentifier(view.state.descriptors[array.Namespace()].Name()) + "." + quoteIdentifier(array.Name())
			} else {
				declaration = strings.TrimSuffix(declaration, "[]") + strings.Repeat("[]", int(d.dimensions))
			}
		}
		collation := ""
		if base.Collation != base.Type.Usage().Collation {
			name := builtinCollationName(base.Collation.ID)
			if name == "" {
				return nil, fmt.Errorf("stuntgres: unsupported domain collation")
			}
			collation = " COLLATE " + quoteIdentifier(view.state.descriptors[ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}].Name()) + "." + quoteIdentifier(name)
		}
		qualified, er := catalogTypeDeclaration(view, d.typ.Usage())
		if er != nil {
			return nil, er
		}
		owner, er := roles.Descriptor(d.owner)
		if er != nil {
			return nil, er
		}
		result = append(result, dumpDomain{owner.Name, "CREATE DOMAIN " + qualified + " AS " + declaration + collation + ";\n"})
	}
	return result, nil
}

func catalogLiteralSQL(ctx context.Context, view CatalogRead, n literalExpression) (string, error) {
	u := n.ResultUsage()
	if n.value.Type() != u.Type {
		return "", fmt.Errorf("stuntgres: invalid stored literal")
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
		literal, err := catalogLiteralSQL(ctx, view, n)
		if err != nil {
			return "", err
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
	}
	if u.Type.IsArray() {
		declaration, err := catalogTypeDeclaration(view, u)
		if err != nil {
			return "", err
		}
		literal := "NULL"
		if !n.value.IsNull() {
			text, err := u.Type.OutputWithEnvironment(ctx, n.value, sqltype.OutputEnvironment{Regclass: catalogRegclassOutput{view: view}})
			if err != nil {
				return "", err
			}
			literal = "'" + strings.ReplaceAll(text, "'", "''") + "'"
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
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
