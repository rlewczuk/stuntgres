package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/publication"
)

// VisitPublicationTables bridges domain resolution to the low catalog producer.
// The caller holds Engine.Mu and supplies its exact captured authorization root;
// the callback only serializes borrowed immutable facts, never external I/O.
func (r EngineCatalogRuntime) VisitPublicationTables(ctx context.Context, view catalog.Read, p *catalog.Publication, visit func(pgcatalog.PublicationTableFact) error) error {
	if ctx == nil || r.engine == nil || r.Auth.Owner != &r.engine.Catalog.Owner || view.Owner() != r.Auth.Owner || view != r.Auth.Catalog || p == nil || view.Get(p.Address()) != p || visit == nil {
		return fmt.Errorf("stuntgres: invalid publication membership capture")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var failure error
	err := publication.VisitTables(view, p, func(relation *catalog.Relation) bool {
		var fact pgcatalog.PublicationTableFact
		fact, failure = publicationTableMetadata(ctx, view, p, relation)
		if failure != nil {
			return false
		}
		failure = visit(fact)
		return failure == nil
	})
	if err != nil {
		return err
	}
	if failure != nil {
		return failure
	}
	return ctx.Err()
}

// publicationTableMetadata projects the selected relation's direct mapping.
// DML-effective ancestry coverage must not suppress its inquiry metadata.
func publicationTableMetadata(ctx context.Context, view catalog.Read, p *catalog.Publication, relation *catalog.Relation) (pgcatalog.PublicationTableFact, error) {
	fact := pgcatalog.PublicationTableFact{Relation: relation}
	if err := ctx.Err(); err != nil {
		return fact, err
	}
	if p == nil || relation == nil || view.Get(p.Address()) != p || view.Get(relation.Address()) != relation {
		return fact, ids.ErrCatalogReplaced
	}
	broad := p.AllTables
	if !broad {
		for _, namespace := range view.PublicationNamespaces() {
			if err := ctx.Err(); err != nil {
				return fact, err
			}
			if namespace.Publication == p.Address() && namespace.Schema == relation.Namespace() {
				broad = true
				break
			}
		}
	}
	var mapping *catalog.PublicationRelation
	if !broad {
		for _, candidate := range view.PublicationRelations() {
			if err := ctx.Err(); err != nil {
				return fact, err
			}
			if candidate.Publication == p.Address() && candidate.Relation == relation.Address() {
				if err := (catalog.PublicationRelationProvider{}).Validate(view, candidate); err != nil {
					return fact, err
				}
				mapping = candidate
				break
			}
		}
	}
	if mapping != nil {
		fact.Filter = mapping.Filter
		if mapping.ColumnsSpecified {
			fact.Columns = mapping.Columns
			return fact, ctx.Err()
		}
	}
	// Borrow unchanged attributes; empty projections need no backing allocation.
	count := 0
	for _, sub := range relation.Attributes {
		if err := ctx.Err(); err != nil {
			return fact, err
		}
		generation, generated := relation.ColumnMeta.Generations[sub]
		if !generated || generation.Kind == catalog.GenerationStored && p.GeneratedColumns == 's' {
			count++
		}
	}
	if count == len(relation.Attributes) {
		fact.Columns = relation.Attributes
	} else if count > 0 {
		fact.Columns = make([]int32, 0, count)
		for _, sub := range relation.Attributes {
			if err := ctx.Err(); err != nil {
				return fact, err
			}
			generation, generated := relation.ColumnMeta.Generations[sub]
			if !generated || generation.Kind == catalog.GenerationStored && p.GeneratedColumns == 's' {
				fact.Columns = append(fact.Columns, sub)
			}
		}
	}
	return fact, ctx.Err()
}
