package control

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

type foreignDefaultStatement struct {
	Site castConsumer
	SQL  string
}

// Foreign declarations are not physical DumpRelations. Split only defaults
// whose retained recipes conflict inside their one CREATE FOREIGN TABLE;
// independent foreign-only deferral must never enable ordinary DeferDefaults.
func (s *dumpScript) separateForeignCastConsumers(ctx context.Context) error {
	for _, descriptor := range s.CatalogView.Descriptors() {
		r, ok := descriptor.(*catalog.Relation)
		if !ok || r.Foreign == nil {
			continue
		}
		type pair struct{ source, target sqltype.ID }
		seen := make(map[pair]catalog.Cast)
		conflicts := make(map[pair]bool)
		collect := func(d catalog.Cast) {
			key := pair{d.Source, d.Target}
			if old, ok := seen[key]; ok && !sameReplayCast(&old, &d) {
				conflicts[key] = true
			}
			seen[key] = d
		}
		oid := r.Address().ObjectID
		for attribute, definition := range r.ColumnMeta.Defaults {
			if !s.ForeignDefaults[oid][attribute] {
				replayCasts(definition.Expression, collect)
			}
		}
		for _, generation := range r.ColumnMeta.Generations {
			replayCasts(generation.Expression, collect)
		}
		for _, check := range r.Constraints.Check {
			replayCasts(check.Expression, collect)
		}
		if len(conflicts) == 0 {
			continue
		}
		selected := s.ForeignDefaults[oid]
		for attribute, definition := range r.ColumnMeta.Defaults {
			affected := false
			replayCasts(definition.Expression, func(d catalog.Cast) {
				affected = affected || conflicts[pair{d.Source, d.Target}]
			})
			if affected {
				if selected == nil {
					selected = make(map[int32]bool)
				}
				selected[attribute] = true
			}
		}
		if len(selected) == 0 {
			continue // Indivisible generation/CHECK conflicts remain errors.
		}
		if s.ForeignDefaults == nil {
			s.ForeignDefaults = make(map[ids.OID]map[int32]bool)
		}
		s.ForeignDefaults[oid] = selected
		statement, err := fdw.DumpTable(ctx, s.CatalogView, s.DomainRoles, r, selected)
		if err != nil {
			return err
		}
		s.ForeignDeclarations[oid] = statement
		c := dumpCollection{Capture: s.Capture}
		edges, err := c.tableDumpPredecessors(r, &table.DumpRelation{DeferredDefinitions: selected})
		if err != nil {
			return err
		}
		server, err := (Resolver{}).Predecessor(s.CatalogView, r.Foreign.Server, r.Address())
		if err != nil {
			return err
		}
		if !slices.Contains(edges.inline, server) {
			edges.inline = append(edges.inline, server)
		}
		s.Graph[dump.Event{OID: oid}] = dump.DeclarationEvents(edges.inline)
	}
	return nil
}
