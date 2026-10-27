package control

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
)

// relationDumpContext is what every relation entry needs from the shared
// prelude: the relation's namespace and its owner's role name.
type relationDumpContext struct {
	namespace *catalog.Namespace
	owner     string
}

// dumpRelationCollector is one entry of the relation Collect table. As in
// dumpCollectors, the first matching entry handles the relation.
type dumpRelationCollector struct {
	name    string // for tests and error context only
	matches func(*catalog.Relation) bool
	collect func(context.Context, *dumpCollection, ids.ObjectAddress, *catalog.Relation, relationDumpContext) error
}

// dumpRelationCollectors lists the relation kinds in check order. The last
// entry, ordinary and partitioned tables, matches every relation.
var dumpRelationCollectors = [...]dumpRelationCollector{
	{"foreign table", isDumpForeignRelation, collectForeignRelationDump},
	{"composite", isDumpComposite, collectCompositeDump},
	{"index", isDumpIndex, collectIndexDump},
	{"view", isDumpView, collectViewDump},
	{"sequence", isDumpSequence, collectSequenceDump},
	{"table", isDumpTable, collectTableDump},
}

// collectRelationDump runs the checks shared by every relation kind, then
// hands the relation to the first matching relation entry.
func collectRelationDump(ctx context.Context, c *dumpCollection, a ids.ObjectAddress, d catalog.Descriptor) error {
	r := d.(*catalog.Relation)
	if r.RelationKind == catalog.CatalogRelation {
		return nil
	}
	ns, ok := c.source.State.Namespace(r.Namespace())
	if !ok {
		return fmt.Errorf("stuntgres: relation namespace is missing")
	}
	if ns.Temporary != (ids.SessionToken{}) {
		return nil
	}
	principal, err := c.DomainRoles.Descriptor(r.Owner)
	if err != nil {
		return err
	}
	place := relationDumpContext{namespace: ns, owner: principal.Name}
	for _, collector := range dumpRelationCollectors {
		if collector.matches(r) {
			return collector.collect(ctx, c, a, r, place)
		}
	}
	return nil
}

func isDumpComposite(r *catalog.Relation) bool { return r.RelationKind == catalog.CompositeRelation }
func isDumpIndex(r *catalog.Relation) bool     { return r.SQLIndex != nil }
func isDumpView(r *catalog.Relation) bool      { return r.View != nil }
func isDumpSequence(r *catalog.Relation) bool  { return r.RelationKind == catalog.SequenceRelation }
func isDumpTable(*catalog.Relation) bool       { return true }

func collectCompositeDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, r *catalog.Relation, _ relationDumpContext) error {
	if typ, ok := c.CatalogView.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.RowType)}); ok && typ.Extension != (ids.ObjectAddress{}) {
		return nil
	}
	c.Composites[r.Address().ObjectID] = r
	c.Graph[dump.Event{OID: r.Address().ObjectID}] = nil
	return nil
}

func collectIndexDump(ctx context.Context, c *dumpCollection, _ ids.ObjectAddress, r *catalog.Relation, place relationDumpContext) error {
	ns := place.namespace
	// Key declarations and their table constraints must use the same
	// captured private-name projection, including deferred local keys.
	r = c.CatalogView.MustRelation(r.Address())
	virtual := index.PartitionReplayVirtualUniqueRoot(c.CatalogView, r)
	if virtual != nil && virtual.Address() != r.Address() {
		return nil
	}
	if r.SQLIndex.Constraint != nil && !index.PartitionReplaySeparateConstraint(c.CatalogView, r) {
		return nil
	}
	var sql string
	var err error
	if virtual != nil {
		sql, err = partition.VirtualUniqueDefinitionSQL(c.CatalogView, r)
	} else {
		sql, err = index.DefinitionSQL(ctx, c.CatalogView, r, index.Presentation{Dump: true})
	}
	if err != nil {
		return err
	}
	oid := r.Address().ObjectID
	if index.PartitionReplayUsingIndex(c.CatalogView, r) {
		parent := c.CatalogView.MustRelation(r.SQLIndex.Parent)
		kind := "UNIQUE"
		if r.SQLIndex.Constraint.Primary {
			kind = "PRIMARY KEY"
		}
		sql += "ALTER TABLE " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(parent.Name()) + " ADD CONSTRAINT " + catalog.QuoteIdentifier(r.Name()) + " " + kind + " USING INDEX " + catalog.QuoteIdentifier(r.Name()) + ";\n"
	}
	event := dump.Event{OID: oid}
	c.Graph[event] = []dump.Event{{OID: r.SQLIndex.Parent.ObjectID}}
	for _, key := range r.SQLIndex.Keys {
		if key.Class.OID() != 0 {
			predecessor, err := Resolver{}.Predecessor(c.CatalogView, ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(key.Class.OID())}, r.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				c.Graph[event] = append(c.Graph[event], dump.Event{OID: predecessor})
			}
		}
		if key.Derived == nil {
			continue
		}
		for _, dependency := range key.Derived.Dependencies {
			predecessor, err := Resolver{}.Predecessor(c.CatalogView, dependency.Address, r.SQLIndex.Parent)
			if err != nil {
				return err
			}
			source := dump.Event{OID: predecessor}
			if predecessor != 0 && !slices.Contains(c.Graph[event], source) {
				c.Graph[event] = append(c.Graph[event], source)
			}
		}
	}
	c.IndexIndices[oid] = len(c.Indexes)
	c.Indexes = append(c.Indexes, OwnedStatement{place.owner, sql})
	return nil
}

func collectViewDump(ctx context.Context, c *dumpCollection, a ids.ObjectAddress, r *catalog.Relation, place relationDumpContext) error {
	ns := place.namespace
	// Materialized definitions preserve their analyzed presentation
	// without synthetic source aliases. Their explicit empty visibility
	// path below qualifies user types for portable, executable dump SQL.
	presentation := stored.Presentation{Columns: r.Columns, Deparse: r.RelationKind == catalog.MaterializedViewRelation}
	if presentation.Deparse {
		// A nil path is diagnostic naming's unqualified mode. Dump's
		// canonical materialized renderer must qualify user types too.
		presentation.Path = []ids.ObjectAddress{}
	}
	body, err := stored.ViewOf(r).SelectSQL(ctx, c.CatalogView, presentation)
	if err != nil {
		return err
	}
	var labels []string
	for _, column := range r.Columns {
		labels = append(labels, catalog.QuoteIdentifier(column.Name))
	}
	columns := ""
	if len(labels) > 0 {
		columns = " (" + strings.Join(labels, ", ") + ")"
	}
	options := ""
	if len(stored.ViewOf(r).Options()) != 0 {
		installed := make([]string, len(stored.ViewOf(r).Options()))
		for i, option := range stored.ViewOf(r).Options() {
			name, value, _ := strings.Cut(option, "=")
			installed[i] = catalog.QuoteIdentifier(name) + "=" + dump.CommentLiteral(value)
		}
		options = " WITH (" + strings.Join(installed, ", ") + ")"
	}
	sql := "CREATE VIEW " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(r.Name()) + columns +
		options + " AS " + body + ";\n"
	if r.RelationKind == catalog.MaterializedViewRelation {
		sql = "CREATE MATERIALIZED VIEW " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(r.Name()) + columns + " AS " + body + " WITH NO DATA;\n"
		stored := c.source.Relations[a]
		if stored == nil {
			return fmt.Errorf("stuntgres: materialized view storage is missing")
		}
		c.Materialized = append(c.Materialized, table.DumpRelation{Owner: place.owner, Schema: ns.Name(), Name: r.Name(), Columns: r.Columns, Attributes: r.Attributes, Versions: stored.Versions, Ends: stored.Ends, Frontier: c.source.Frontier, Descriptor: r})
	}
	// Capture every source occurrence and expression dependency from the
	// same immutable catalog used to render the definition. Attribute
	// references depend on their parent relation, not its column OID.
	predecessors := make(map[ids.OID]bool)
	addPredecessor := func(address ids.ObjectAddress) error {
		predecessor, err := Resolver{}.Predecessor(c.OriginalCatalog, address, r.Address())
		if err != nil {
			return err
		}
		if predecessor != 0 {
			predecessors[predecessor] = true
		}
		return nil
	}
	for _, address := range r.AutoExtensions {
		extension, ok := c.source.State.Extension(address)
		if !ok {
			return fmt.Errorf("stuntgres: missing materialized extension dump provider")
		}
		if err := addPredecessor(address); err != nil {
			return err
		}
		sql += "ALTER MATERIALIZED VIEW " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(r.Name()) + " DEPENDS ON EXTENSION " + catalog.QuoteIdentifier(extension.Name()) + ";\n"
	}
	for _, leaf := range stored.ViewOf(r).Sources() {
		if err := addPredecessor(leaf.Ref.Address); err != nil {
			return err
		}
	}
	for _, dependency := range stored.ViewOf(r).Dependencies() {
		if err := addPredecessor(dependency.Ref.Address); err != nil {
			return err
		}
	}
	for _, literal := range stored.ViewOf(r).Literals() {
		if err := regobj.ClassReplayReferences(c.OriginalCatalog, literal, addPredecessor); err != nil {
			return err
		}
	}
	if r.RelationKind == catalog.MaterializedViewRelation {
		// ViewIndices also contains non-relation declarations. Capture options
		// with the actual descriptor and names, not a later event-OID lookup.
		qualified := catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(r.Name())
		sql += dumpRelationOptionsSQL(qualified, r.StorageOptions)
	}
	oid := r.Address().ObjectID
	event := dump.Event{OID: oid}
	c.Graph[event] = make([]dump.Event, 0, len(predecessors))
	for predecessor := range predecessors {
		c.Graph[event] = append(c.Graph[event], dump.Event{OID: predecessor})
	}
	c.ViewIndices[oid] = len(c.Views)
	c.Views = append(c.Views, OwnedStatement{place.owner, sql})
	return nil
}

func collectSequenceDump(_ context.Context, c *dumpCollection, a ids.ObjectAddress, r *catalog.Relation, place relationDumpContext) error {
	g := c.source.Sequences[a]
	if g == nil {
		return fmt.Errorf("stuntgres: sequence state missing")
	}
	entry := sequence.Dump{Owner: place.owner, Schema: place.namespace.Name(), Name: r.Name(), Options: *r.Sequence, State: *g, Comment: r.Comment}
	if owned := r.Sequence.Owned; owned != (ids.ObjectAddress{}) {
		tableAddress := owned
		tableAddress.SubID = 0
		table, ok := c.source.State.Relation(tableAddress)
		if !ok {
			return fmt.Errorf("stuntgres: sequence ownership target missing")
		}
		tableSchema := c.source.State.Get(table.Namespace()).Name()
		entry.OwnedTable = catalog.QuoteIdentifier(tableSchema) + "." + catalog.QuoteIdentifier(table.Name())
		for i, sub := range table.Attributes {
			if sub == owned.SubID {
				entry.OwnedColumn = table.Columns[i].Name
				if r.Sequence.Identity {
					entry.Divergent = sequence.IdentityDumpDivergent(*r.Sequence, table.Columns[i].Usage.Type.ID())
				}
			}
		}
	}
	c.Sequences = append(c.Sequences, entry)
	return nil
}

func collectTableDump(_ context.Context, c *dumpCollection, a ids.ObjectAddress, r *catalog.Relation, place relationDumpContext) error {
	ns := place.namespace
	stored := c.source.Relations[a]
	if stored == nil && r.Partition == nil {
		return fmt.Errorf("stuntgres: relation storage is missing")
	}
	oid := r.Address().ObjectID
	edges, err := c.tableDumpPredecessors(r, nil)
	if err != nil {
		return err
	}
	predecessors := edges.inline
	c.Graph[dump.Event{OID: oid}] = dump.DeclarationEvents(predecessors)
	if len(edges.deferred) != 0 && !c.DeferDefaults {
		if c.InlinePredecessors == nil {
			c.InlinePredecessors = make(map[ids.OID][]ids.OID)
		}
		c.InlinePredecessors[oid] = predecessors
		combined := slices.Clone(predecessors)
		for _, predecessor := range edges.deferred {
			if !slices.Contains(combined, predecessor) {
				combined = append(combined, predecessor)
			}
		}
		c.Graph[dump.Event{OID: oid}] = dump.DeclarationEvents(combined)
	}
	c.TableIndices[oid] = len(c.Tables)
	linked := make(map[ids.ObjectAddress]sequence.Dump, len(r.ColumnMeta.Identities))
	for _, identity := range r.ColumnMeta.Identities {
		relation := c.source.State.MustRelation(identity.Sequence)
		linked[identity.Sequence] = sequence.Dump{Owner: place.owner, Schema: ns.Name(), Name: relation.Name(), Options: *relation.Sequence, State: *c.source.Sequences[identity.Sequence], Comment: relation.Comment}
	}
	var versions []storage.RowVersion
	var ends *storage.DeletionRoot
	if stored != nil {
		versions, ends = stored.Versions, stored.Ends
	}
	c.Tables = append(c.Tables, table.DumpRelation{Owner: place.owner, Schema: ns.Name(), Name: r.Name(), Columns: r.Columns, Attributes: r.Attributes, Versions: versions, Ends: ends, Frontier: c.source.Frontier, Comment: r.Comment, ColumnComments: r.ColumnMeta.Comments, NotNullConstraints: r.Constraints.NotNull, Defaults: r.ColumnMeta.Defaults, DeferredDefinitions: edges.deferredDefinitions, Descriptor: r, Identities: r.ColumnMeta.Identities, IdentitySequences: linked})
	if selected := c.source.RecipePlan[r.Address()]; selected != nil {
		c.Tables[len(c.Tables)-1].RecipePlaceholders = true
		c.Tables[len(c.Tables)-1].RecipeComponent = selected.RecipeComponent
		c.Tables[len(c.Tables)-1].DeferredChecks = selected.DeferredChecks
	}
	return nil
}

// tableDumpEdges are a table declaration's predecessors: those its CREATE
// needs inline, and those only its deferrable defaults need.
type tableDumpEdges struct {
	inline, deferred    []ids.OID
	deferredDefinitions map[int32]bool
}

// tableDumpPredecessors collects a table declaration's predecessors from its
// parents, partition bounds, checks, defaults and generations, in that
// order. Ordinary defaults may enable global deferral; eligible foreign
// defaults always replay after declarations without changing that decision.
func (c *dumpCollection) tableDumpPredecessors(r *catalog.Relation, replay *table.DumpRelation) (tableDumpEdges, error) {
	var predecessors []ids.OID
	if r.Inheritance != nil {
		for _, edge := range r.Inheritance.Parents {
			parent := edge.Relation
			predecessor, err := Resolver{}.Predecessor(c.CatalogView, parent, r.Address())
			if err != nil {
				return tableDumpEdges{}, err
			}
			predecessors = append(predecessors, predecessor)
		}
	}
	predecessors, err := partition.DumpPredecessors(Resolver{}, c.CatalogView, r, predecessors)
	if err != nil {
		return tableDumpEdges{}, err
	}
	for _, check := range r.Constraints.Check {
		if check.NotValid || replay != nil && replay.DeferredChecks[check.Name] || c.source.RecipePlan[r.Address()] != nil && c.source.RecipePlan[r.Address()].DeferredChecks[check.Name] {
			continue
		}
		add := func(address ids.ObjectAddress) error {
			predecessor, err := Resolver{}.Predecessor(c.CatalogView, address, r.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
				predecessors = append(predecessors, predecessor)
			}
			return nil
		}
		if err := regobj.ClassReplayReferences(c.CatalogView, check.Expression, add); err != nil {
			return tableDumpEdges{}, err
		}
		for _, dependency := range check.Dependencies {
			if err := add(dependency.Ref.Address); err != nil {
				return tableDumpEdges{}, err
			}
		}
	}
	var deferredPredecessors []ids.OID
	var deferredDefinitions map[int32]bool
	for attribute, definition := range r.ColumnMeta.Defaults {
		eligible := table.DefaultReplayDeferrable(definition) || replay != nil && replay.DeferredDefinitions[attribute]
		if eligible {
			if deferredDefinitions == nil {
				deferredDefinitions = make(map[int32]bool)
			}
			deferredDefinitions[attribute] = true
			if r.Foreign != nil {
				continue
			}
			c.DeferDefaults = c.DeferDefaults || definition.Expression.ResultUsage().Type.ContainsRegclass()
		}
		addPredecessor := func(address ids.ObjectAddress) error {
			owner := r.Address()
			if eligible {
				owner = ids.ObjectAddress{}
			}
			predecessor, err := Resolver{}.Predecessor(c.CatalogView, address, owner)
			if err != nil {
				return err
			}
			if eligible {
				if !c.DeferDefaults && predecessor != 0 && !slices.Contains(deferredPredecessors, predecessor) {
					deferredPredecessors = append(deferredPredecessors, predecessor)
				}
			} else if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
				predecessors = append(predecessors, predecessor)
			}
			return nil
		}
		// Opaque literal payloads need replay ordering even when their BOOL/
		// TEXT result is inline and intentionally has no relation DROP edge.
		if err := regobj.ClassReplayReferences(c.CatalogView, definition.Expression, addPredecessor); err != nil {
			return tableDumpEdges{}, err
		}
		for _, dependency := range definition.Dependencies {
			if err := addPredecessor(dependency.Address); err != nil {
				return tableDumpEdges{}, err
			}
		}
	}
	for _, definition := range r.ColumnMeta.Generations {
		if c.source.RecipePlan[r.Address()] != nil || replay != nil && replay.RecipePlaceholders {
			continue
		}
		for _, dependency := range definition.Dependencies {
			predecessor, err := Resolver{}.Predecessor(c.CatalogView, dependency.Address, r.Address())
			if err != nil {
				return tableDumpEdges{}, err
			}
			if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
				predecessors = append(predecessors, predecessor)
			}
		}
	}
	return tableDumpEdges{inline: predecessors, deferred: deferredPredecessors, deferredDefinitions: deferredDefinitions}, nil
}
