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
		return collationSQLName(view, uint32(definitions[i].oid)) < collationSQLName(view, uint32(definitions[j].oid))
	})
	statements := make([]dumpDomain, 0, len(definitions))
	literal := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	for _, d := range definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mode, providerErr := collationSemantics(d)
		if providerErr != nil || mode != d.semantics {
			return nil, fmt.Errorf("stuntgres: invalid dump collation provider")
		}
		owner, err := roles.Descriptor(d.owner)
		if err != nil {
			return nil, err
		}
		name := collationSQLName(view, uint32(d.oid))
		var definition strings.Builder
		definition.WriteString("CREATE COLLATION " + name)
		// PostgreSQL has no ENCODING attribute. Immutable bootstrap copies
		// preserve exceptional encodings without retaining user-source edges.
		bootstrap := uint32(0)
		if d.provider == "c" && d.encoding == -1 && d.deterministic && d.rules == nil && d.version == nil && d.locale == nil && d.collate != nil && d.ctype != nil && *d.collate == *d.ctype {
			switch *d.collate {
			case "C":
				bootstrap = 950
			case "POSIX":
				bootstrap = 951
			}
		}
		if d.provider == "b" && d.encoding == 6 && d.deterministic && d.rules == nil && d.version != nil && *d.version == "1" && d.collate == nil && d.ctype == nil && d.locale != nil && *d.locale == "C" {
			bootstrap = 962
		}
		if bootstrap != 0 {
			definition.WriteString(" FROM " + collationSQLName(view, bootstrap) + ";\n")
		} else {
			encoding := int32(6)
			if d.provider == "b" && d.locale != nil && *d.locale == "C" {
				encoding = -1
			}
			if (d.provider == "c" || d.provider == "b") && d.encoding != encoding {
				return nil, fmt.Errorf("stuntgres: dump cannot preserve collation encoding and version")
			}
			provider := "libc"
			if d.provider == "b" {
				provider = "builtin"
			}
			if d.provider == "i" {
				provider = "icu"
			}
			definition.WriteString(" (PROVIDER = " + provider + ", DETERMINISTIC = ")
			if d.deterministic {
				definition.WriteString("true")
			} else {
				definition.WriteString("false")
			}
			for _, option := range []struct {
				name  string
				value *string
			}{{"LC_COLLATE", d.collate}, {"LC_CTYPE", d.ctype}, {"LOCALE", d.locale}, {"RULES", d.rules}, {"VERSION", d.version}} {
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
