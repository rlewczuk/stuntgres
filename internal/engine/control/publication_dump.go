package control

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/publication"
)

// Publication replay is rendered in preflight, against the immutable original
// catalog and role roots. Emission neither reads live state nor binds filters.
type publicationReplay struct{ owner, name, sql string }

func isDumpPublication(d catalog.Descriptor) bool {
	switch d.(type) {
	case *catalog.Publication, *catalog.PublicationRelation, *catalog.PublicationNamespace:
		return true
	}
	return false
}

func collectPublicationDump(ctx context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch d := d.(type) {
	case *catalog.Publication:
		if err := (catalog.PublicationProvider{}).Validate(c.CatalogView, d); err != nil {
			return err
		}
		if err := access.ValidateACL(c.OriginalRoles, d); err != nil {
			return err
		}
		c.Publications = append(c.Publications, d)
	case *catalog.PublicationRelation:
		if err := (catalog.PublicationRelationProvider{}).Validate(c.CatalogView, d); err != nil {
			return err
		}
		c.PublicationRelations = append(c.PublicationRelations, d)
	case *catalog.PublicationNamespace:
		if err := (catalog.PublicationNamespaceProvider{}).Validate(c.CatalogView, d); err != nil {
			return err
		}
		c.PublicationNamespaces = append(c.PublicationNamespaces, d)
	default:
		return fmt.Errorf("stuntgres: invalid publication dump descriptor")
	}
	return nil
}

func (s *dumpScript) preparePublications(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(s.Publications) == 0 && len(s.PublicationRelations) == 0 && len(s.PublicationNamespaces) == 0 {
		return nil
	}
	read := s.OriginalCatalog
	bootstrap, err := s.OriginalRoles.Descriptor(read.Owner().Principals.Principal(10))
	if err != nil {
		return err
	}
	if !bootstrap.Superuser {
		return fmt.Errorf("stuntgres: dump publication bootstrap authority is unavailable")
	}
	// Keep explicit edges, including redundant schema-covered edges. Expanding
	// effective tables would lose dynamic membership and absent column lists.
	tables := make(map[ids.ObjectAddress][]string)
	for _, m := range s.PublicationRelations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := (catalog.PublicationRelationProvider{}).Validate(read, m); err != nil {
			return err
		}
		r := read.MustRelation(m.Relation)
		if !publication.IsPublishable(read, r) {
			return fmt.Errorf("stuntgres: invalid publication dump relation")
		}
		ns := read.MustNamespace(r.Namespace())
		var b strings.Builder
		// ONLY prevents replay from adding ordinary inheritance descendants that
		// were created after the original explicit membership was recorded.
		b.WriteString("ONLY " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(r.Name()))
		if m.ColumnsSpecified {
			b.WriteString(" (")
			for i, attribute := range m.Columns {
				if i != 0 {
					b.WriteString(", ")
				}
				position := slices.Index(r.Attributes, attribute)
				if position < 0 || position >= len(r.Columns) {
					return ids.ErrCatalogReplaced
				}
				b.WriteString(catalog.QuoteIdentifier(r.Columns[position].Name))
			}
			b.WriteString(")")
		}
		if m.Filter != nil {
			text, err := publication.FilterSQL(ctx, read, m)
			if err != nil {
				return err
			}
			b.WriteString(" WHERE (" + text + ")")
		}
		tables[m.Publication] = append(tables[m.Publication], b.String())
	}
	schemas := make(map[ids.ObjectAddress][]string)
	for _, m := range s.PublicationNamespaces {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := (catalog.PublicationNamespaceProvider{}).Validate(read, m); err != nil {
			return err
		}
		ns := read.MustNamespace(m.Schema)
		if ns.Temporary != (ids.SessionToken{}) || ns.Address().ObjectID == ids.PGCatalogNamespaceOID || ns.Address().ObjectID == ids.PGToastNamespaceOID {
			return fmt.Errorf("stuntgres: invalid publication dump schema")
		}
		schemas[m.Publication] = append(schemas[m.Publication], catalog.QuoteIdentifier(ns.Name()))
	}
	var replay []publicationReplay
	for _, p := range s.Publications {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := (catalog.PublicationProvider{}).Validate(read, p); err != nil {
			return err
		}
		if err := access.ValidateACL(s.OriginalRoles, p); err != nil {
			return err
		}
		owner, err := s.OriginalRoles.Descriptor(p.Owner)
		if err != nil {
			return err
		}
		name := catalog.QuoteIdentifier(p.Name())
		var b strings.Builder
		b.WriteString("CREATE PUBLICATION " + name)
		if p.AllTables {
			b.WriteString(" FOR ALL TABLES")
		}
		var actions []string
		for _, action := range []struct {
			enabled bool
			name    string
		}{{p.Insert, "insert"}, {p.Update, "update"}, {p.Delete, "delete"}, {p.Truncate, "truncate"}} {
			if action.enabled {
				actions = append(actions, action.name)
			}
		}
		generated := "none"
		if p.GeneratedColumns == 's' {
			generated = "stored"
		}
		fmt.Fprintf(&b, " WITH (publish = '%s', publish_via_partition_root = %t, publish_generated_columns = %s);\n", strings.Join(actions, ", "), p.ViaRoot, generated)
		tableNames, schemaNames := tables[p.Address()], schemas[p.Address()]
		slices.Sort(tableNames)
		slices.Sort(schemaNames)
		if len(tableNames) != 0 {
			b.WriteString("ALTER PUBLICATION " + name + " ADD TABLE " + strings.Join(tableNames, ", ") + ";\n")
		}
		if len(schemaNames) != 0 {
			b.WriteString("ALTER PUBLICATION " + name + " ADD TABLES IN SCHEMA " + strings.Join(schemaNames, ", ") + ";\n")
		}
		b.WriteString("ALTER PUBLICATION " + name + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n")
		replay = append(replay, publicationReplay{owner: bootstrap.Name, name: name, sql: b.String()})
		delete(tables, p.Address())
		delete(schemas, p.Address())
	}
	if len(tables) != 0 || len(schemas) != 0 {
		return fmt.Errorf("stuntgres: publication dump membership has no captured publication")
	}
	slices.SortFunc(replay, func(a, b publicationReplay) int { return strings.Compare(a.name, b.name) })
	s.publicationReplay = replay
	return nil
}

func emitDumpPublications(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	for _, r := range s.publicationReplay {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.setOwner(r.owner); err != nil {
			return err
		}
		if err := w.emit(r.sql); err != nil {
			return err
		}
	}
	return nil
}
