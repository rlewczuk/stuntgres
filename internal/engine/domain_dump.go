package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/typedef"
)

func domainDumpStatement(ctx context.Context, p dump.Presenter, view catalog.Read, roles authorization.Read, d *catalog.Type, replayNames *typedef.DomainReplayNames) (dump.Statement, error) {
	if err := ctx.Err(); err != nil {
		return dump.Statement{}, err
	}
	if !typedef.DefaultDomainACL(d) {
		return dump.Statement{}, fmt.Errorf("stuntgres: dump cannot serialize changed domain ACL")
	}
	qualified, err := p.TypeDeclaration(view, nil, d.SQLType.Usage())
	if err != nil {
		return dump.Statement{}, err
	}
	owner, err := roles.Descriptor(d.Owner)
	if err != nil {
		return dump.Statement{}, err
	}
	if d.SQLType.IsEnum() {
		labels := d.SQLType.EnumLabels()
		for i, label := range labels {
			labels[i] = "'" + strings.ReplaceAll(label, "'", "''") + "'"
		}
		return dump.Statement{Owner: owner.Name, SQL: "CREATE TYPE " + qualified + " AS ENUM (" + strings.Join(labels, ", ") + ");\n"}, nil
	}
	base := d.SQLType.DomainBase()
	declaration, err := p.TypeDeclaration(view, nil, base)
	if err != nil {
		return dump.Statement{}, err
	}
	if base.Type.IsArray() {
		if d.Dimensions == 0 {
			key, exists := replayNames.Names[base.Type.ID()]
			if !exists || replayNames.Arrays[base.Type.Element().ID()] != base.Type.ID() {
				return dump.Statement{}, fmt.Errorf("stuntgres: missing domain replay array identity")
			}
			namespace, exists := view.Descriptor(key.Namespace)
			if !exists {
				return dump.Statement{}, fmt.Errorf("stuntgres: missing domain replay array namespace")
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
			return dump.Statement{}, fmt.Errorf("stuntgres: unsupported domain collation")
		}
		collation = " COLLATE " + name
	}
	var checks strings.Builder
	if d.DomainDefaultOwn {
		text, err := p.DomainDefault(ctx, view, d.DomainDefault.Expression)
		if err != nil {
			return dump.Statement{}, err
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
		text, err := p.DomainCheck(ctx, view, base, check.Expression)
		if err != nil {
			return dump.Statement{}, err
		}
		checks.WriteString(" CONSTRAINT " + catalog.Identifier(check.Name) + " CHECK (" + text + ")")
	}
	return dump.Statement{Owner: owner.Name, SQL: "CREATE DOMAIN " + qualified + " AS " + declaration + collation + checks.String() + ";\n"}, nil
}

func domainInvalidDumpStatement(ctx context.Context, p dump.Presenter, view catalog.Read, domain *catalog.Type) (string, error) {
	qualified, err := p.TypeDeclaration(view, nil, domain.SQLType.Usage())
	if err != nil {
		return "", err
	}
	base := domain.SQLType.DomainBase()
	var text strings.Builder
	for _, check := range domain.DomainChecks {
		if !check.NotValid {
			continue
		}
		expression, err := p.DomainCheck(ctx, view, base, check.Expression)
		if err != nil {
			return "", err
		}
		text.WriteString("ALTER DOMAIN " + qualified + " ADD CONSTRAINT " + catalog.Identifier(check.Name) + " CHECK (" + expression + ") NOT VALID;\n")
	}
	return text.String(), nil
}
