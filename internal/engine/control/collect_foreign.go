package control

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/table"
)

func isDumpForeign(d catalog.Descriptor) bool        { _, ok := d.(*catalog.ForeignObject); return ok }
func isDumpForeignRelation(r *catalog.Relation) bool { return r.Foreign != nil }

func collectForeignDump(_ context.Context, c *dumpCollection, a ids.ObjectAddress, d catalog.Descriptor) error {
	object := d.(*catalog.ForeignObject)
	if object.Origin != nil {
		return (catalog.ForeignCatalogProvider{Class: object.Address().ClassID}).Validate(c.CatalogView, object)
	}
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

func collectForeignRelationDump(ctx context.Context, c *dumpCollection, a ids.ObjectAddress, r *catalog.Relation, place relationDumpContext) error {
	edges, err := c.tableDumpPredecessors(r, nil)
	if err != nil {
		return err
	}
	statement, err := fdw.DumpTable(ctx, c.CatalogView, c.DomainRoles, r, "")
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
	if place.namespace == nil { place.namespace = c.CatalogView.MustNamespace(r.Namespace()) }
	if place.owner == "" {
		owner, err := c.DomainRoles.Descriptor(r.Owner)
		if err != nil { return err }
		place.owner = owner.Name
	}
	var linked map[ids.ObjectAddress]sequence.Dump
	if len(r.ColumnMeta.Identities) != 0 { linked = make(map[ids.ObjectAddress]sequence.Dump, len(r.ColumnMeta.Identities)) }
	for _, identity := range r.ColumnMeta.Identities {
		relation := c.CatalogView.MustRelation(identity.Sequence)
		linked[identity.Sequence] = sequence.Dump{Schema: c.CatalogView.Get(relation.Namespace()).Name(), Name: relation.Name(), Options: *relation.Sequence}
	}
	if c.ForeignTables == nil { c.ForeignTables = make(map[ids.OID]table.DumpRelation) }
	c.ForeignTables[a.ObjectID] = table.DumpRelation{Owner: place.owner, Schema: place.namespace.Name(), Name: r.Name(), Columns: r.Columns, Attributes: r.Attributes, Comment: r.Comment, ColumnComments: r.ColumnMeta.Comments, NotNullConstraints: r.Constraints.NotNull, Defaults: r.ColumnMeta.Defaults, DeferredDefinitions: edges.deferredDefinitions, Descriptor: r, Identities: r.ColumnMeta.Identities, IdentitySequences: linked}
	// The declaration installs the complete logical relation. Hierarchy
	// completion waits for it, not a nonexistent foreign row-storage phase.
	c.Graph[dump.Event{OID: a.ObjectID, Phase: dump.TableCompletion}] = []dump.Event{{OID: a.ObjectID}}
	return nil
}

func (c *dumpCollection) addForeignDeclaration(oid ids.OID, statement dump.Statement, predecessors []dump.Event) {
	if c.ForeignDeclarations == nil {
		c.ForeignDeclarations = make(map[ids.OID]dump.Statement)
	}
	c.ForeignDeclarations[oid] = statement
	c.Graph[dump.Event{OID: oid}] = predecessors
}

func foreignTableClause(view catalog.Read, r *catalog.Relation) (string, error) {
	if r.Foreign == nil { return "", nil }
	server, ok := view.Foreign(r.Foreign.Server)
	if !ok || server.Address().ClassID != ids.ForeignServerClassID { return "", fmt.Errorf("stuntgres: missing foreign server") }
	return " SERVER " + catalog.QuoteIdentifier(server.Name()) + fdw.DumpOptions(r.Foreign.Options), nil
}

func emitDumpForeignTableCompletion(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	if _, foreign := s.ForeignTables[event.OID]; !foreign { return nil }
	t := s.table(event.OID)
	if err := w.setOwner("postgres"); err != nil { return err }
	if err := emitRelationColumnStorage(s, w, t.Descriptor); err != nil { return err }
	if err := t.EmitInheritedDefaults(ctx, s.OriginalCatalog, true, func(attribute int32, text string) error {
		site := castConsumer{Event: dump.Event{OID: event.OID}, Attribute: attribute}
		if s.attributeSchedules[t.Descriptor.Address()] != nil { site.Event.Phase = dump.AttributeCompletion }
		return s.emitBound(w, site, text)
	}); err != nil { return err }
	if err := t.EmitNoInheritNotNull(w.emit, w.setOwner); err != nil { return err }
	return w.emit(s.declarations[event.OID].FollowingSQL)
}

func (s *dumpScript) foreignTableDeclaration(ctx context.Context, oid ids.OID) (string, error) {
	t := s.table(oid)
	if schedule := s.attributeSchedules[t.Descriptor.Address()]; schedule != nil {
		return s.stagedTableCreateSQL(ctx, &t, schedule.creates[t.Descriptor.Address()])
	}
	return dumpTableCreateSQL(ctx, s, &t, false)
}
