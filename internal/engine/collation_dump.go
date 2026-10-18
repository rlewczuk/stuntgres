package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/authorization"
)

func collationDumpStatements(ctx context.Context, view CatalogRead, roles authorization.Read, definitions []*collationDescriptor) ([]dumpDomain, error) {
	sort.Slice(definitions, func(i, j int) bool {
		return collationSQLName(view, uint32(definitions[i].OID)) < collationSQLName(view, uint32(definitions[j].OID))
	})
	statements := make([]dumpDomain, 0, len(definitions))
	literal := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	for _, d := range definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mode, providerErr := collationSemantics(d)
		if providerErr != nil || mode != d.Semantics {
			return nil, fmt.Errorf("stuntgres: invalid dump collation provider")
		}
		owner, err := roles.Descriptor(d.Owner)
		if err != nil {
			return nil, err
		}
		name := collationSQLName(view, uint32(d.OID))
		var definition strings.Builder
		definition.WriteString("CREATE COLLATION " + name)
		// PostgreSQL has no ENCODING attribute. Immutable bootstrap copies
		// preserve exceptional encodings without retaining user-source edges.
		bootstrap := uint32(0)
		if d.Provider == "c" && d.Encoding == -1 && d.Deterministic && d.Rules == nil && d.Version == nil && d.Locale == nil && d.Collate != nil && d.CType != nil && *d.Collate == *d.CType {
			switch *d.Collate {
			case "C":
				bootstrap = 950
			case "POSIX":
				bootstrap = 951
			}
		}
		if d.Provider == "b" && d.Encoding == 6 && d.Deterministic && d.Rules == nil && d.Version != nil && *d.Version == "1" && d.Collate == nil && d.CType == nil && d.Locale != nil && *d.Locale == "C" {
			bootstrap = 962
		}
		if bootstrap != 0 {
			definition.WriteString(" FROM " + collationSQLName(view, bootstrap) + ";\n")
		} else {
			encoding := int32(6)
			if d.Provider == "b" && d.Locale != nil && *d.Locale == "C" {
				encoding = -1
			}
			if (d.Provider == "c" || d.Provider == "b") && d.Encoding != encoding {
				return nil, fmt.Errorf("stuntgres: dump cannot preserve collation encoding and version")
			}
			provider := "libc"
			if d.Provider == "b" {
				provider = "builtin"
			}
			if d.Provider == "i" {
				provider = "icu"
			}
			definition.WriteString(" (PROVIDER = " + provider + ", DETERMINISTIC = ")
			if d.Deterministic {
				definition.WriteString("true")
			} else {
				definition.WriteString("false")
			}
			for _, option := range []struct {
				name  string
				value *string
			}{{"LC_COLLATE", d.Collate}, {"LC_CTYPE", d.CType}, {"LOCALE", d.Locale}, {"RULES", d.Rules}, {"VERSION", d.Version}} {
				if option.value != nil {
					definition.WriteString(", " + option.name + " = " + literal(*option.value))
				}
			}
			definition.WriteString(");\n")
		}
		statements = append(statements, dumpDomain{owner: owner.Name, sql: definition.String()})
	}
	return statements, nil
}
