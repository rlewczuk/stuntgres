package control

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
)

// The staged layout changes statement boundaries, not captured definitions.
// The existing deparsers and declaration predicates remain the authority.
func (s *dumpScript) emitStagedTableConstraints(ctx context.Context, w *dumpWriter, t *table.DumpRelation, step uint32) error {
	d := t.Descriptor
	qualified := catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name)
	prefix := "ALTER TABLE " + qualified + " ADD CONSTRAINT "
	if step == 1 {
		for _, constraint := range t.NotNullConstraints {
			if constraint.NotValid || !table.DumpNotNullDeclaration(s.OriginalCatalog, d, constraint.Attribute) {
				continue
			}
			if d.Inheritance == nil || d.Inheritance.Columns[constraint.Attribute].Local {
				continue
			} // Own column ADD already declared it.
			i := d.IndexForAttribute(constraint.Attribute)
			if err := w.emit(prefix + catalog.QuoteIdentifier(constraint.Name) + " NOT NULL " + catalog.QuoteIdentifier(t.Columns[i].Name) + ";\n"); err != nil {
				return err
			}
		}
		for _, check := range table.OrderedChecks(d) {
			if check.NotValid || t.DeferredChecks[check.Name] || !table.DumpCheckDeclaration(s.OriginalCatalog, d, check) {
				continue
			}
			text, err := (stored.ScalarRenderer{Owner: d, CheckReplayPrecision: true}).SQL(ctx, s.CatalogView, check.Expression)
			if err != nil {
				return err
			}
			clause := " CHECK (" + text + ")"
			if check.NoInherit {
				clause += " NO INHERIT"
			}
			site := castConsumer{Event: dump.Event{OID: d.Address().ObjectID, Phase: dump.AttributeCompletion, Step: 1}, Check: check.Name}
			if err := s.emitBound(w, site, prefix+catalog.QuoteIdentifier(check.Name)+clause+";\n"); err != nil {
				return err
			}
		}
	}
	if step == 0 {
		for _, constraint := range d.Constraints.Unique {
			if table.PhysicalPrimaryDeferred(d, &constraint) {
				continue
			}
			idx, ok := s.CatalogView.Relation(constraint.Index)
			if !ok {
				return fmt.Errorf("stuntgres: missing unique dump index")
			}
			if _, moved, err := table.PhysicalCompetingIndexSource(s.OriginalCatalog, idx); err != nil {
				return err
			} else if moved {
				continue
			}
			if index.PartitionReplaySeparateConstraint(s.CatalogView, idx) {
				continue
			}
			text, err := (stored.Deparser{View: s.CatalogView}).UniqueConstraintSQL(d, &constraint)
			if err != nil {
				return err
			}
			if err := w.emit(prefix + catalog.QuoteIdentifier(constraint.Name) + " " + text + ";\n"); err != nil {
				return err
			}
		}
		for _, constraint := range d.Constraints.Exclusion {
			if constraint.Parent != 0 {
				continue
			}
			idx, ok := s.CatalogView.Relation(constraint.Index)
			if !ok {
				return fmt.Errorf("stuntgres: missing exclusion dump index")
			}
			if index.PartitionReplaySeparateConstraint(s.CatalogView, idx) {
				continue
			}
			text, err := index.DefinitionSQL(ctx, s.CatalogView, idx, index.Presentation{Dump: true, Exclusion: true})
			if err != nil {
				return err
			}
			if err := w.emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + catalog.QuoteIdentifier(constraint.Name) + " " + text + ";\n"); err != nil {
				return err
			}
		}
	}
	if step == 2 {
		for _, constraint := range d.Constraints.ForeignKeys {
			if constraint.NotValid || table.PhysicalSelfForeignKeyDeferred(d, constraint) || table.InheritedForeignKeyDeferred(s.OriginalCatalog, d, &constraint) {
				continue
			}
			text, err := (stored.Deparser{View: s.CatalogView}).ForeignKeyConstraintSQL(d, constraint)
			if err != nil {
				return err
			}
			if err := w.emit(prefix + catalog.QuoteIdentifier(constraint.Name) + " " + text + ";\n"); err != nil {
				return err
			}
		}
	}
	return nil
}
