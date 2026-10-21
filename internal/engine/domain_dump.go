package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/typedef"
)

type dumpDomain struct{ owner, sql string }

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
