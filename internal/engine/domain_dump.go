package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

type dumpDomain struct{ owner, sql string }

// Catalog rendering resolves the captured namespace, including renamed schemas;
// the immutable codec never consults a mutable engine to format a domain name.
func catalogTypeDeclaration(view catalog.Read, u sqltype.Usage) (string, error) {
	return catalogTypeDeclarationAt(view, nil, u)
}

func catalogTypeDeclarationAt(view catalog.Read, reader definitionReader, u sqltype.Usage) (string, error) {
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
	if !ok || descriptor.(*catalog.Type).SQLType != u.Type || !u.Valid() {
		return "", fmt.Errorf("stuntgres: invalid domain declaration authority")
	}
	namespace, ok := definitionDescriptorAt(view, reader, descriptor.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing domain namespace")
	}
	return catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(descriptor.Name()), nil
}

// Column declarations retain syntactic rank separately from array type identity.
// A copied rank-zero array uses its catalog name, without adding bracket syntax.
func catalogColumnDeclaration(view catalog.Read, column catalog.Column) (string, error) {
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
	alias := catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(descriptor.Name())
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

func domainDumpStatement(ctx context.Context, view catalog.Read, roles authorization.Read, d *catalog.Type, replayNames *typedef.DomainReplayNames) (dumpDomain, error) {
	if err := ctx.Err(); err != nil {
		return dumpDomain{}, err
	}
	if !typedef.DefaultDomainACL(d) {
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
			key, exists := replayNames.Names[base.Type.ID()]
			if !exists || replayNames.Arrays[base.Type.Element().ID()] != base.Type.ID() {
				return dumpDomain{}, fmt.Errorf("stuntgres: missing domain replay array identity")
			}
			namespace, exists := view.Descriptor(key.Namespace)
			if !exists {
				return dumpDomain{}, fmt.Errorf("stuntgres: missing domain replay array namespace")
			}
			declaration = catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(key.Name)
		} else {
			declaration = strings.TrimSuffix(declaration, "[]") + strings.Repeat("[]", int(d.Dimensions))
		}
	}
	collation := ""
	if base.Collation != base.Type.Usage().Collation {
		name := typedef.CollationSQLName(view, base.Collation.ID)
		if name == "" {
			return dumpDomain{}, fmt.Errorf("stuntgres: unsupported domain collation")
		}
		collation = " COLLATE " + name
	}
	var checks strings.Builder
	if d.DomainDefaultOwn {
		text, err := (scalarRenderer{currentRowShape: true}).sql(ctx, view, d.DomainDefault.Expression)
		if err != nil {
			return dumpDomain{}, err
		}
		checks.WriteString(" DEFAULT " + text)
	}
	if d.SQLType.OwnDomainNotNull() {
		checks.WriteString(" NOT NULL")
	}
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

func catalogLiteralSQL(ctx context.Context, view catalog.Read, n literalExpression) (string, error) {
	return catalogLiteralWithACL(ctx, view, n, aclItemContextFormatter(ctx))
}

func catalogLiteralWithACL(ctx context.Context, view catalog.Read, n literalExpression, aclOutput sqltype.ACLItemFormatter) (string, error) {
	return catalogLiteralWithACLAt(ctx, view, nil, n, aclOutput)
}

func catalogLiteralWithACLAt(ctx context.Context, view catalog.Read, reader definitionReader, n literalExpression, aclOutput sqltype.ACLItemFormatter) (string, error) {
	u := n.ResultUsage()
	if n.Value.Type() != u.Type {
		return "", fmt.Errorf("stuntgres: invalid stored literal")
	}
	if u.Type.IsEnum() || u.Type.IsQueryInt() || u.Type.IsCube() {
		declaration, err := catalogTypeDeclarationAt(view, reader, u)
		if err != nil {
			return "", err
		}
		literal, err := u.Type.SQLLiteral(ctx, n.Value)
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
		value, err := n.Value.BaseValue()
		if err != nil {
			return "", err
		}
		n.Usage = u.BaseUsage()
		n.Value = value
		literal, err := catalogLiteralWithACLAt(ctx, view, reader, n, aclOutput)
		if err != nil {
			return "", err
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
	}
	if !n.Value.IsNull() && u.Type.ID() == sqltype.ACLItemArray && n.Value.ArrayRank() == 1 && n.Value.ArrayDimension(0) == (sqltype.ArrayDimension{Lower: 1, Length: 0}) {
		return `pg_catalog.acldefault('c'::"char",0::oid)`, nil
	}
	// Composite text input discards the special empty ACL shape. Retain typed
	// member expressions instead, including domain wrappers at every depth.
	if !n.Value.IsNull() && u.Type.IsComposite() && containsEmptyACL(n.Value) {
		declaration, err := catalogTypeDeclarationAt(view, reader, u)
		if err != nil {
			return "", err
		}
		parts := make([]string, n.Value.CompositeLen())
		for i := range parts {
			member := n.Value.CompositeField(i)
			literal := literalExpression{Info: expr.Info{Usage: u.Type.CompositeMember(i).Usage}, Value: member}
			parts[i], err = catalogLiteralWithACLAt(ctx, view, reader, literal, aclOutput)
			if err != nil {
				return "", err
			}
		}
		return "CAST(ROW(" + strings.Join(parts, ", ") + ") AS " + declaration + ")", nil
	}
	if !n.Value.IsNull() && u.Type.IsArray() && u.Type.ID() != sqltype.ACLItemArray && containsEmptyACL(n.Value) {
		return "", fmt.Errorf("stuntgres: dump cannot serialize rank-one empty ACL inside an array of composite carriers")
	}
	if sqltype.IsRoutineAlias(u.Type.ID()) && !n.Value.IsNull() {
		return routineLiteralSQL(view, n.Value)
	}
	if sqltype.IsOperatorAlias(u.Type.ID()) && !n.Value.IsNull() {
		return operatorLiteralSQL(view, n.Value)
	}
	if u.Type.IsArray() || u.Type.IsComposite() {
		declaration, err := catalogTypeDeclarationAt(view, reader, u)
		if err != nil {
			return "", err
		}
		if (u.Type.ContainsRegproc() || u.Type.ContainsOperatorAlias()) && !n.Value.IsNull() {
			return routineContainerLiteralAt(ctx, view, reader, n.Value, u, aclOutput)
		}
		literal := "NULL"
		if !n.Value.IsNull() {
			output := dumpCatalogAliasOutput{view: view}
			text, err := u.Type.OutputWithEnvironment(ctx, n.Value, sqltype.OutputEnvironment{Regclass: catalogRegclassOutput{view: view, definition: reader}, Regtype: output, Regcollation: output, Regnamespace: output, Regrole: regroleContextFormatter(ctx), Operator: dumpOperatorOutput{}, Regproc: catalogRoutineOutput{view: view, qualified: true}, RegTS: dumpRegTSOutput{view: view}, ACLItem: aclOutput})
			if err != nil {
				return "", err
			}
			literal = "'" + strings.ReplaceAll(text, "'", "''") + "'"
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
	}
	if u.Type.ID() == sqltype.ACLItem && !n.Value.IsNull() {
		text, err := u.Type.OutputWithEnvironment(ctx, n.Value, sqltype.OutputEnvironment{ACLItem: aclOutput})
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS ACLITEM)", nil
	}
	if u.Type.ID() == sqltype.Regrole && !n.Value.IsNull() {
		text, err := u.Type.OutputWithEnvironment(ctx, n.Value, sqltype.OutputEnvironment{Regrole: regroleContextFormatter(ctx)})
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS REGROLE)", nil
	}
	if sqltype.IsRegTS(u.Type.ID()) && !n.Value.IsNull() {
		id, err := n.Value.RegTSOID()
		if err != nil {
			return "", err
		}
		text := regTSSpelling(view, nil, id, u.Type.ID(), true)
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS " + u.Type.SQLName() + ")", nil
	}
	if u.Type.ID() == sqltype.Regtype && !n.Value.IsNull() {
		id, err := n.Value.TypeOID()
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(regtypeReference(view, id), "'", "''") + "' AS REGTYPE)", nil
	}
	if sqltype.IsRegObjectAlias(u.Type.ID()) && !n.Value.IsNull() {
		output := dumpCatalogAliasOutput{view: view}
		text, err := u.Type.OutputWithEnvironment(ctx, n.Value, sqltype.OutputEnvironment{Regcollation: output, Regnamespace: output})
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS " + u.Type.SQLName() + ")", nil
	}
	if u.Type.ID() == sqltype.Regclass && !n.Value.IsNull() {
		text, err := n.Value.Text()
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
	view       catalog.Read
	path       []ids.ObjectAddress
	definition definitionReader
}

func (r catalogRegclassOutput) FormatRegclass(id uint32) (string, error) {
	return regclassSpellingAt(r.view, r.definition, r.path, ids.OID(id)), nil
}
