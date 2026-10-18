package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type dumpDomain struct{ owner, sql string }

// Replay names are transient rendering predictions keyed by captured identity,
// not a catalog or destination OID allocator. Only domain/enum creators run here.
type domainReplayNames struct {
	occupied map[ids.NameKey]sqltype.ID
	names    map[sqltype.ID]ids.NameKey
	arrays   map[sqltype.ID]sqltype.ID
	elements map[sqltype.ID]sqltype.ID
}

func newDomainReplayNames() *domainReplayNames {
	r := &domainReplayNames{
		occupied: make(map[ids.NameKey]sqltype.ID), names: make(map[sqltype.ID]ids.NameKey),
		arrays: make(map[sqltype.ID]sqltype.ID), elements: make(map[sqltype.ID]sqltype.ID),
	}
	for _, typ := range sqltype.Builtins() {
		namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		if typ.NamespaceName() == "information_schema" {
			namespace.ObjectID = 13229
		}
		key := ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: typ.Name()}
		r.occupied[key], r.names[typ.ID()] = typ.ID(), key
		r.arrays[typ.ID()] = typ.AssociatedArray()
		if typ.IsArray() {
			r.elements[typ.ID()] = typ.Element().ID()
		}
	}
	return r
}

func (r *domainReplayNames) arrayName(key ids.NameKey) ids.NameKey {
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
	r.occupied[key], r.names[d.SQLType.ID()] = d.SQLType.ID(), key
	return nil
}

func (r *domainReplayNames) associate(d *typeDescriptor) {
	if d.Array == 0 {
		return
	}
	key := r.arrayName(r.names[d.SQLType.ID()])
	r.occupied[key], r.names[d.Array] = d.Array, key
	r.arrays[d.SQLType.ID()], r.elements[d.Array] = d.Array, d.SQLType.ID()
}

func defaultDomainACL(d *typeDescriptor) bool {
	if !d.ACLSyntheticDefault || !d.ACL.Explicit {
		return false
	}
	var public, owner, options authorization.Privileges
	for _, entry := range d.ACL.Entries {
		if entry.Grantor != d.Owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (authorization.PrincipalRef{}) || entry.GrantOptions != 0 {
				return false
			}
			public |= entry.Privileges
		} else {
			if entry.Grantee.Role != d.Owner {
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
	return catalogTypeDeclarationAt(view, nil, u)
}

func catalogTypeDeclarationAt(view CatalogRead, reader definitionReader, u sqltype.Usage) (string, error) {
	if u.Type.IsArray() {
		array, ok := definitionArrayAt(view, reader, u.Type.Element())
		if !ok || array != u.Type || !u.Valid() {
			return "", fmt.Errorf("stuntgres: invalid array declaration authority")
		}
		element := u
		element.Type = u.Type.Element()
		declaration, err := catalogTypeDeclarationAt(view, reader, element)
		if err != nil {
			return "", err
		}
		return declaration + "[]", nil
	}
	if !u.Type.IsUserDefined() && !(u.Type.IsDomain() && u.Type.Concrete()) {
		return u.Declaration()
	}
	descriptor, ok := definitionDescriptorAt(view, reader, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})
	if !ok || descriptor.(*typeDescriptor).SQLType != u.Type || !u.Valid() {
		return "", fmt.Errorf("stuntgres: invalid domain declaration authority")
	}
	namespace, ok := definitionDescriptorAt(view, reader, descriptor.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing domain namespace")
	}
	return quoteIdentifier(namespace.Name()) + "." + quoteIdentifier(descriptor.Name()), nil
}

// Column declarations retain syntactic rank separately from array type identity.
// A copied rank-zero array uses its catalog name, without adding bracket syntax.
func catalogColumnDeclaration(view CatalogRead, column ColumnDescriptor) (string, error) {
	declaration, err := catalogTypeDeclaration(view, column.Usage)
	if err != nil || !column.Usage.Type.IsArray() {
		return declaration, err
	}
	if column.DeclaredDimensions > 0 {
		return declaration + strings.Repeat("[]", int(column.DeclaredDimensions)-1), nil
	}
	descriptor, ok := view.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(column.Usage.Type.ID())})
	if !ok {
		return "", fmt.Errorf("stuntgres: missing array declaration identity")
	}
	namespace, ok := view.Descriptor(descriptor.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing array declaration namespace")
	}
	alias := quoteIdentifier(namespace.Name()) + "." + quoteIdentifier(descriptor.Name())
	if column.Usage.Typmod >= 0 {
		if column.Usage.Type.Element().ID() == sqltype.Interval {
			rangeMask, precision := column.Usage.Typmod>>16, column.Usage.Typmod&0xffff
			alias += "(" + strconv.FormatInt(int64(rangeMask), 10)
			if precision != sqltype.IntervalFullPrecision {
				alias += "," + strconv.FormatInt(int64(precision), 10)
			}
			alias += ")"
		} else {
			start, end := strings.IndexByte(declaration, '('), strings.LastIndexByte(declaration, ')')
			if start < 0 || end < start {
				return "", fmt.Errorf("stuntgres: missing array declaration modifier")
			}
			alias += declaration[start : end+1]
		}
	}
	return alias, nil
}

func domainDumpStatement(ctx context.Context, view CatalogRead, roles authorization.Read, d *typeDescriptor, replayNames *domainReplayNames) (dumpDomain, error) {
	if err := ctx.Err(); err != nil {
		return dumpDomain{}, err
	}
	if !defaultDomainACL(d) {
		return dumpDomain{}, fmt.Errorf("stuntgres: dump cannot serialize changed domain ACL")
	}
	qualified, err := catalogTypeDeclaration(view, d.SQLType.Usage())
	if err != nil {
		return dumpDomain{}, err
	}
	owner, err := roles.Descriptor(d.Owner)
	if err != nil {
		return dumpDomain{}, err
	}
	if d.SQLType.IsEnum() {
		labels := d.SQLType.EnumLabels()
		for i, label := range labels {
			labels[i] = "'" + strings.ReplaceAll(label, "'", "''") + "'"
		}
		return dumpDomain{owner.Name, "CREATE TYPE " + qualified + " AS ENUM (" + strings.Join(labels, ", ") + ");\n"}, nil
	}
	base := d.SQLType.DomainBase()
	declaration, err := catalogTypeDeclaration(view, base)
	if err != nil {
		return dumpDomain{}, err
	}
	if base.Type.IsArray() {
		if d.Dimensions == 0 {
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
			declaration = strings.TrimSuffix(declaration, "[]") + strings.Repeat("[]", int(d.Dimensions))
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
	var checks strings.Builder
	for _, check := range d.DomainChecks {
		if check.NotValid {
			continue
		}
		text, err := (scalarRenderer{domainValue: &base}).sql(ctx, view, check.Expression)
		if err != nil {
			return dumpDomain{}, err
		}
		checks.WriteString(" CONSTRAINT " + pgIdentifier(check.Name) + " CHECK (" + text + ")")
	}
	return dumpDomain{owner.Name, "CREATE DOMAIN " + qualified + " AS " + declaration + collation + checks.String() + ";\n"}, nil
}

func catalogLiteralSQL(ctx context.Context, view CatalogRead, n literalExpression) (string, error) {
	return catalogLiteralWithACL(ctx, view, n, aclItemContextFormatter(ctx))
}

func catalogLiteralWithACL(ctx context.Context, view CatalogRead, n literalExpression, aclOutput sqltype.ACLItemFormatter) (string, error) {
	return catalogLiteralWithACLAt(ctx, view, nil, n, aclOutput)
}

func catalogLiteralWithACLAt(ctx context.Context, view CatalogRead, reader definitionReader, n literalExpression, aclOutput sqltype.ACLItemFormatter) (string, error) {
	u := n.ResultUsage()
	if n.value.Type() != u.Type {
		return "", fmt.Errorf("stuntgres: invalid stored literal")
	}
	if u.Type.IsEnum() || u.Type.IsQueryInt() {
		declaration, err := catalogTypeDeclarationAt(view, reader, u)
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
		declaration, err := catalogTypeDeclarationAt(view, reader, u)
		if err != nil {
			return "", err
		}
		value, err := n.value.BaseValue()
		if err != nil {
			return "", err
		}
		n.Usage = u.BaseUsage()
		n.value = value
		literal, err := catalogLiteralWithACLAt(ctx, view, reader, n, aclOutput)
		if err != nil {
			return "", err
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
	}
	if !n.value.IsNull() && u.Type.ID() == sqltype.ACLItemArray && n.value.ArrayRank() == 1 && n.value.ArrayDimension(0) == (sqltype.ArrayDimension{Lower: 1, Length: 0}) {
		return `pg_catalog.acldefault('c'::"char",0::oid)`, nil
	}
	// Composite text input discards the special empty ACL shape. Retain typed
	// member expressions instead, including domain wrappers at every depth.
	if !n.value.IsNull() && u.Type.IsComposite() && containsEmptyACL(n.value) {
		declaration, err := catalogTypeDeclarationAt(view, reader, u)
		if err != nil {
			return "", err
		}
		parts := make([]string, n.value.CompositeLen())
		for i := range parts {
			member := n.value.CompositeField(i)
			literal := literalExpression{Info: expr.Info{Usage: u.Type.CompositeMember(i).Usage}, value: member}
			parts[i], err = catalogLiteralWithACLAt(ctx, view, reader, literal, aclOutput)
			if err != nil {
				return "", err
			}
		}
		return "CAST(ROW(" + strings.Join(parts, ", ") + ") AS " + declaration + ")", nil
	}
	if !n.value.IsNull() && u.Type.IsArray() && u.Type.ID() != sqltype.ACLItemArray && containsEmptyACL(n.value) {
		return "", fmt.Errorf("stuntgres: dump cannot serialize rank-one empty ACL inside an array of composite carriers")
	}
	if sqltype.IsRoutineAlias(u.Type.ID()) && !n.value.IsNull() {
		return routineLiteralSQL(view, n.value)
	}
	if sqltype.IsOperatorAlias(u.Type.ID()) && !n.value.IsNull() {
		return operatorLiteralSQL(view, n.value)
	}
	if u.Type.IsArray() || u.Type.IsComposite() {
		declaration, err := catalogTypeDeclarationAt(view, reader, u)
		if err != nil {
			return "", err
		}
		if (u.Type.ContainsRegproc() || u.Type.ContainsOperatorAlias()) && !n.value.IsNull() {
			return routineContainerLiteralAt(ctx, view, reader, n.value, u, aclOutput)
		}
		literal := "NULL"
		if !n.value.IsNull() {
			output := dumpCatalogAliasOutput{view: view}
			text, err := u.Type.OutputWithEnvironment(ctx, n.value, sqltype.OutputEnvironment{Regclass: catalogRegclassOutput{view: view, definition: reader}, Regtype: output, Regcollation: output, Regnamespace: output, Regrole: regroleContextFormatter(ctx), Operator: dumpOperatorOutput{}, Regproc: catalogRoutineOutput{view: view, qualified: true}, RegTS: dumpRegTSOutput{view: view}, ACLItem: aclOutput})
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
	if sqltype.IsRegTS(u.Type.ID()) && !n.value.IsNull() {
		id, err := n.value.RegTSOID()
		if err != nil {
			return "", err
		}
		text := regTSSpelling(view, nil, id, u.Type.ID(), true)
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS " + u.Type.SQLName() + ")", nil
	}
	if u.Type.ID() == sqltype.Regtype && !n.value.IsNull() {
		id, err := n.value.TypeOID()
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(regtypeReference(view, id), "'", "''") + "' AS REGTYPE)", nil
	}
	if sqltype.IsRegObjectAlias(u.Type.ID()) && !n.value.IsNull() {
		output := dumpCatalogAliasOutput{view: view}
		text, err := u.Type.OutputWithEnvironment(ctx, n.value, sqltype.OutputEnvironment{Regcollation: output, Regnamespace: output})
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS " + u.Type.SQLName() + ")", nil
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
		return "CAST('" + strings.ReplaceAll(regclassSpellingAt(view, reader, nil, ids.OID(id)), "'", "''") + "' AS REGCLASS)", nil
	}
	return literalSQL(ctx, n)
}

type catalogRegclassOutput struct {
	view       CatalogRead
	path       []ids.ObjectAddress
	definition definitionReader
}

func (r catalogRegclassOutput) FormatRegclass(id uint32) (string, error) {
	return regclassSpellingAt(r.view, r.definition, r.path, ids.OID(id)), nil
}
