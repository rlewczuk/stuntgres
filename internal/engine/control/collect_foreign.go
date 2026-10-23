package control

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
)

func isDumpForeign(d catalog.Descriptor) bool        { _, ok := d.(*catalog.ForeignObject); return ok }
func isDumpForeignRelation(r *catalog.Relation) bool { return r.Foreign != nil }

func collectForeignDump(_ context.Context, c *dumpCollection, a ids.ObjectAddress, d catalog.Descriptor) error {
	object := d.(*catalog.ForeignObject)
	statement, err := fdw.DumpObject(c.CatalogView, c.DomainRoles, object)
	if err != nil {
		return err
	}
	var predecessors []dump.Event
	for _, dependency := range []ids.ObjectAddress{object.Wrapper, object.Server, object.Handler, object.Validator} {
		if dependency == (ids.ObjectAddress{}) {
			continue
		}
		oid, err := (Resolver{}).Predecessor(c.CatalogView, dependency, a)
		if err != nil {
			return err
		}
		if oid != 0 {
			predecessors = append(predecessors, dump.Event{OID: oid})
		}
	}
	c.addForeignDeclaration(a.ObjectID, statement, predecessors)
	return nil
}

func collectForeignRelationDump(ctx context.Context, c *dumpCollection, a ids.ObjectAddress, r *catalog.Relation, _ relationDumpContext) error {
	edges, err := c.tableDumpPredecessors(r, nil)
	if err != nil {
		return err
	}
	statement, err := fdw.DumpTable(ctx, c.CatalogView, c.DomainRoles, r, edges.deferredDefinitions)
	if err != nil {
		return err
	}
	if len(edges.deferredDefinitions) != 0 {
		if c.ForeignDefaults == nil {
			c.ForeignDefaults = make(map[ids.OID]map[int32]bool)
		}
		c.ForeignDefaults[a.ObjectID] = edges.deferredDefinitions
	}
	predecessors := edges.inline
	server, err := (Resolver{}).Predecessor(c.CatalogView, r.Foreign.Server, a)
	if err != nil {
		return err
	}
	if !slices.Contains(predecessors, server) {
		predecessors = append(predecessors, server)
	}
	c.addForeignDeclaration(a.ObjectID, statement, dump.DeclarationEvents(predecessors))
	return nil
}

func (c *dumpCollection) addForeignDeclaration(oid ids.OID, statement dump.Statement, predecessors []dump.Event) {
	if c.ForeignDeclarations == nil {
		c.ForeignDeclarations = make(map[ids.OID]dump.Statement)
	}
	c.ForeignDeclarations[oid] = statement
	c.Graph[dump.Event{OID: oid}] = predecessors
}
