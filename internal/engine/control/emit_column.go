package control

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// writeDumpColumn is shared by CREATE and source-shaped staged ADD. It renders
// the same captured declaration, deferral decisions and inherited recipe flags.
func writeDumpColumn(ctx context.Context, s *dumpScript, t *table.DumpRelation, i int, typed bool, line *strings.Builder) error {
	c := t.Columns[i]
	provenance := t.ColumnProvenance(t.Attributes[i])
	line.WriteString(catalog.QuoteIdentifier(c.Name))
	if typed {
		line.WriteString(" WITH OPTIONS")
	} else {
		declaration, err := regobj.ColumnDeclaration(s.CatalogView, c)
		if err != nil {
			return err
		}
		line.WriteString(" " + declaration)
		if c.Usage.Collation.ID != c.Usage.Type.Usage().Collation.ID {
			name := typedef.CollationSQLName(s.CatalogView, c.Usage.Collation.ID)
			if name == "" {
				return fmt.Errorf("stuntgres: unsupported stored collation")
			}
			line.WriteString(" COLLATE " + name)
		}
	}
	if definition, exists := t.Defaults[t.Attributes[i]]; exists && provenance.ExplicitDefault && !(s.DeferDefaults && t.DeferredDefinitions[t.Attributes[i]]) {
		text, err := stored.DefaultSQL(ctx, s.CatalogView, definition.Expression)
		if err != nil {
			return err
		}
		line.WriteString(" DEFAULT " + text)
	}
	if provenance.ExplicitDefault && t.Descriptor.Inheritance != nil {
		if _, exists := t.Defaults[t.Attributes[i]]; !exists {
			line.WriteString(" DEFAULT NULL")
		}
	}
	if definition, exists := t.Descriptor.ColumnMeta.Generations[t.Attributes[i]]; exists && provenance.ExplicitGeneration {
		text := "NULL"
		if !t.RecipePlaceholders {
			var err error
			text, err = (stored.ScalarRenderer{Owner: t.Descriptor}).SQL(ctx, s.CatalogView, definition.Expression)
			if err != nil {
				return err
			}
		}
		line.WriteString(" GENERATED ALWAYS AS (" + text + ") " + definition.Kind.SQL())
	}
	if identity, exists := t.Identities[t.Attributes[i]]; exists {
		mode := "ALWAYS"
		if identity.Mode == parser.IdentityByDefault {
			mode = "BY DEFAULT"
		}
		dumped := t.IdentitySequences[identity.Sequence]
		line.WriteString(" GENERATED " + mode + " AS IDENTITY (SEQUENCE NAME " + catalog.QuoteIdentifier(dumped.Schema) + "." + catalog.QuoteIdentifier(dumped.Name))
		if !sequence.IdentityDumpDivergent(dumped.Options, c.Usage.Type.ID()) {
			line.WriteString(" " + sequence.NumericSQL(dumped.Options))
		}
		line.WriteString(")")
	}
	if !c.Nullable && table.DumpNotNullDeclaration(s.OriginalCatalog, t.Descriptor, t.Attributes[i]) {
		for _, constraint := range t.NotNullConstraints {
			if constraint.Attribute == t.Attributes[i] && !constraint.NotValid {
				line.WriteString(" CONSTRAINT " + catalog.QuoteIdentifier(constraint.Name) + " NOT NULL")
				break
			}
		}
	}
	return nil
}
