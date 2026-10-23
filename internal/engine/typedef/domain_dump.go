package typedef

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
)

func domainDumpStatement(ctx context.Context, p dump.Presenter, view catalog.Read, roles authorization.Read, d *catalog.Type, replayNames *DomainReplayNames) (dump.Statement, error) {
	if err := ctx.Err(); err != nil {
		return dump.Statement{}, err
	}
	if !DefaultDomainACL(d) {
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
		name := CollationSQLName(view, base.Collation.ID)
		if name == "" {
			return dump.Statement{}, fmt.Errorf("stuntgres: unsupported domain collation")
		}
		collation = " COLLATE " + name
	}
	var checks strings.Builder
	if d.DomainDefault != nil {
		text, err := p.DomainDefault(ctx, view, d.DomainDefault.Expression)
		if err != nil {
			return dump.Statement{}, err
		}
		checks.WriteString(" DEFAULT " + text)
	} else {
		// Explicit absence must not re-inherit the present-day parent's default.
		checks.WriteString(" DEFAULT NULL")
	}
	if d.SQLType.OwnDomainNotNull() {
		if d.DomainNotNull == nil {
			return dump.Statement{}, fmt.Errorf("stuntgres: missing owned domain NOT NULL definition")
		}
		checks.WriteString(" CONSTRAINT " + catalog.Identifier(d.DomainNotNull.Name) + " NOT NULL")
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
	record, err := CaptureDomainDefault(view, d)
	if err != nil {
		return dump.Statement{}, err
	}
	prefix, err := EncodeDomainDefault(record)
	if err != nil {
		return dump.Statement{}, err
	}
	return dump.Statement{Owner: owner.Name, SQL: prefix + "CREATE DOMAIN " + qualified + " AS " + declaration + collation + checks.String() + ";\n"}, nil
}

// DomainCheckDumpStatement emits one retained CHECK at its replay window.
// Valid checks run ordinary ALTER validation; NOT VALID stays NOT VALID.
func DomainCheckDumpStatement(ctx context.Context, p dump.Presenter, view catalog.Read, domain *catalog.Type, check catalog.DomainCheckConstraint) (string, error) {
	qualified, err := p.TypeDeclaration(view, nil, domain.SQLType.Usage())
	if err != nil {
		return "", err
	}
	expression, err := p.DomainCheck(ctx, view, domain.SQLType.DomainBase(), check.Expression)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	writeDomainCheckDump(&text, qualified, expression, check)
	return text.String(), nil
}

func writeDomainCheckDump(text *strings.Builder, qualified, expression string, check catalog.DomainCheckConstraint) {
	text.WriteString("ALTER DOMAIN " + qualified + " ADD CONSTRAINT " + catalog.Identifier(check.Name) + " CHECK (" + expression + ")")
	if check.NotValid {
		text.WriteString(" NOT VALID")
	}
	text.WriteString(";\n")
}

func DomainInvalidDumpStatement(ctx context.Context, p dump.Presenter, view catalog.Read, domain *catalog.Type) (string, error) {
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
		writeDomainCheckDump(&text, qualified, expression, check)
	}
	return text.String(), nil
}
