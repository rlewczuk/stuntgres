package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
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
	ns, ok := c.source.state.Namespace(r.Namespace())
	if !ok {
		return fmt.Errorf("stuntgres: relation namespace is missing")
	}
	if ns.Temporary != (ids.SessionToken{}) {
		return nil
	}
	if r.Security.ACL.Explicit || len(r.Security.ColumnACL) != 0 {
		return fmt.Errorf("stuntgres: dump cannot serialize explicit relation or column ACL")
	}
	principal, err := c.domainRoles.Descriptor(r.Owner)
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
	c.composites[r.Address().ObjectID] = r
	c.graph[dump.Event{OID: r.Address().ObjectID}] = nil
	return nil
}

func collectIndexDump(ctx context.Context, c *dumpCollection, _ ids.ObjectAddress, r *catalog.Relation, place relationDumpContext) error {
	ns := place.namespace
	// Key declarations and their table constraints must use the same
	// captured private-name projection, including deferred local keys.
	r = c.catalogView.MustRelation(r.Address())
	virtual := index.PartitionReplayVirtualUniqueRoot(c.catalogView, r)
	if virtual != nil && virtual.Address() != r.Address() {
		return nil
	}
	if r.SQLIndex.Constraint != nil && !index.PartitionReplaySeparateConstraint(c.catalogView, r) {
		return nil
	}
	var sql string
	var err error
	if virtual != nil {
		sql, err = partitionVirtualUniqueDefinitionSQL(c.catalogView, r)
	} else {
		sql, err = index.DefinitionSQL(ctx, c.catalogView, r, index.Presentation{Dump: true})
	}
	if err != nil {
		return err
	}
	oid := r.Address().ObjectID
	if index.PartitionReplayUsingIndex(c.catalogView, r) {
		parent := c.catalogView.MustRelation(r.SQLIndex.Parent)
		kind := "UNIQUE"
		if r.SQLIndex.Constraint.Primary {
			kind = "PRIMARY KEY"
		}
		sql += "ALTER TABLE " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(parent.Name()) + " ADD CONSTRAINT " + catalog.QuoteIdentifier(r.Name()) + " " + kind + " USING INDEX " + catalog.QuoteIdentifier(r.Name()) + ";\n"
	}
	event := dump.Event{OID: oid}
	c.graph[event] = []dump.Event{{OID: r.SQLIndex.Parent.ObjectID}}
	for _, key := range r.SQLIndex.Keys {
		if key.Derived == nil {
			continue
		}
		for _, dependency := range key.Derived.Dependencies {
			predecessor, err := dumpPredecessor(c.catalogView, dependency.Address, r.SQLIndex.Parent)
			if err != nil {
				return err
			}
			source := dump.Event{OID: predecessor}
			if predecessor != 0 && !slices.Contains(c.graph[event], source) {
				c.graph[event] = append(c.graph[event], source)
			}
		}
	}
	c.indexIndices[oid] = len(c.indexes)
	c.indexes = append(c.indexes, dumpView{place.owner, sql})
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
	body, err := stored.ViewOf(r).SelectSQL(ctx, c.catalogView, presentation)
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
		stored := c.source.relations[a]
		if stored == nil {
			return fmt.Errorf("stuntgres: materialized view storage is missing")
		}
		c.materialized = append(c.materialized, dumpRelation{owner: place.owner, schema: ns.Name(), name: r.Name(), columns: r.Columns, attributes: r.Attributes, versions: stored.Versions, ends: stored.Ends, frontier: c.source.frontier, descriptor: r})
	}
	// Capture every source occurrence and expression dependency from the
	// same immutable catalog used to render the definition. Attribute
	// references depend on their parent relation, not its column OID.
	predecessors := make(map[ids.OID]bool)
	addPredecessor := func(address ids.ObjectAddress) error {
		predecessor, err := dumpPredecessor(c.originalCatalog, address, r.Address())
		if err != nil {
			return err
		}
		if predecessor != 0 {
			predecessors[predecessor] = true
		}
		return nil
	}
	for _, address := range r.AutoExtensions {
		extension, ok := c.source.state.Extension(address)
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
		if err := regobj.ClassReplayReferences(c.originalCatalog, literal, addPredecessor); err != nil {
			return err
		}
	}
	oid := r.Address().ObjectID
	event := dump.Event{OID: oid}
	c.graph[event] = make([]dump.Event, 0, len(predecessors))
	for predecessor := range predecessors {
		c.graph[event] = append(c.graph[event], dump.Event{OID: predecessor})
	}
	c.viewIndices[oid] = len(c.views)
	c.views = append(c.views, dumpView{place.owner, sql})
	return nil
}

func collectSequenceDump(_ context.Context, c *dumpCollection, a ids.ObjectAddress, r *catalog.Relation, place relationDumpContext) error {
	g := c.source.sequences[a]
	if g == nil {
		return fmt.Errorf("stuntgres: sequence state missing")
	}
	entry := sequence.Dump{Owner: place.owner, Schema: place.namespace.Name(), Name: r.Name(), Options: *r.Sequence, State: *g, Comment: r.Comment}
	if owned := r.Sequence.Owned; owned != (ids.ObjectAddress{}) {
		tableAddress := owned
		tableAddress.SubID = 0
		table, ok := c.source.state.Relation(tableAddress)
		if !ok {
			return fmt.Errorf("stuntgres: sequence ownership target missing")
		}
		tableSchema := c.source.state.Get(table.Namespace()).Name()
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
	c.sequences = append(c.sequences, entry)
	return nil
}

func collectTableDump(_ context.Context, c *dumpCollection, a ids.ObjectAddress, r *catalog.Relation, place relationDumpContext) error {
	ns := place.namespace
	stored := c.source.relations[a]
	if stored == nil && r.Partition == nil {
		return fmt.Errorf("stuntgres: relation storage is missing")
	}
	oid := r.Address().ObjectID
	edges, err := c.tableDumpPredecessors(r)
	if err != nil {
		return err
	}
	predecessors := edges.inline
	c.graph[dump.Event{OID: oid}] = dump.DeclarationEvents(predecessors)
	if len(edges.deferred) != 0 && !c.deferDefaults {
		if c.inlinePredecessors == nil {
			c.inlinePredecessors = make(map[ids.OID][]ids.OID)
		}
		c.inlinePredecessors[oid] = predecessors
		combined := slices.Clone(predecessors)
		for _, predecessor := range edges.deferred {
			if !slices.Contains(combined, predecessor) {
				combined = append(combined, predecessor)
			}
		}
		c.graph[dump.Event{OID: oid}] = dump.DeclarationEvents(combined)
	}
	c.tableIndices[oid] = len(c.tables)
	linked := make(map[ids.ObjectAddress]sequence.Dump, len(r.ColumnMeta.Identities))
	for _, identity := range r.ColumnMeta.Identities {
		relation := c.source.state.MustRelation(identity.Sequence)
		linked[identity.Sequence] = sequence.Dump{Owner: place.owner, Schema: ns.Name(), Name: relation.Name(), Options: *relation.Sequence, State: *c.source.sequences[identity.Sequence], Comment: relation.Comment}
	}
	var versions []storage.RowVersion
	var ends *storage.DeletionRoot
	if stored != nil {
		versions, ends = stored.Versions, stored.Ends
	}
	c.tables = append(c.tables, dumpRelation{owner: place.owner, schema: ns.Name(), name: r.Name(), columns: r.Columns, attributes: r.Attributes, versions: versions, ends: ends, frontier: c.source.frontier, comment: r.Comment, columnComments: r.ColumnMeta.Comments, notNullConstraints: r.Constraints.NotNull, defaults: r.ColumnMeta.Defaults, deferredDefinitions: edges.deferredDefinitions, descriptor: r, identities: r.ColumnMeta.Identities, identitySequences: linked})
	if selected := c.recipePlan[r.Address()]; selected != nil {
		c.tables[len(c.tables)-1].recipePlaceholders = true
		c.tables[len(c.tables)-1].recipeComponent = selected.recipeComponent
		c.tables[len(c.tables)-1].deferredChecks = selected.deferredChecks
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
// order. A deferrable default that yields a regclass sets deferDefaults.
func (c *dumpCollection) tableDumpPredecessors(r *catalog.Relation) (tableDumpEdges, error) {
	var predecessors []ids.OID
	if r.Inheritance != nil {
		for _, parent := range r.Inheritance.Parents {
			predecessor, err := dumpPredecessor(c.catalogView, parent, r.Address())
			if err != nil {
				return tableDumpEdges{}, err
			}
			predecessors = append(predecessors, predecessor)
		}
	}
	predecessors, err := partitionDumpPredecessors(c.catalogView, r, predecessors)
	if err != nil {
		return tableDumpEdges{}, err
	}
	for _, check := range r.Constraints.Check {
		if check.NotValid || c.recipePlan[r.Address()] != nil && c.recipePlan[r.Address()].deferredChecks[check.Name] {
			continue
		}
		add := func(address ids.ObjectAddress) error {
			predecessor, err := dumpPredecessor(c.catalogView, address, r.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
				predecessors = append(predecessors, predecessor)
			}
			return nil
		}
		if err := regobj.ClassReplayReferences(c.catalogView, check.Expression, add); err != nil {
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
		eligible := defaultReplayDeferrable(definition)
		if eligible {
			if deferredDefinitions == nil {
				deferredDefinitions = make(map[int32]bool)
			}
			deferredDefinitions[attribute] = true
			c.deferDefaults = c.deferDefaults || definition.Expression.ResultUsage().Type.ContainsRegclass()
		}
		addPredecessor := func(address ids.ObjectAddress) error {
			owner := r.Address()
			if eligible {
				owner = ids.ObjectAddress{}
			}
			predecessor, err := dumpPredecessor(c.catalogView, address, owner)
			if err != nil {
				return err
			}
			if eligible {
				if !c.deferDefaults && predecessor != 0 && !slices.Contains(deferredPredecessors, predecessor) {
					deferredPredecessors = append(deferredPredecessors, predecessor)
				}
			} else if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
				predecessors = append(predecessors, predecessor)
			}
			return nil
		}
		// Opaque literal payloads need replay ordering even when their BOOL/
		// TEXT result is inline and intentionally has no relation DROP edge.
		if err := regobj.ClassReplayReferences(c.catalogView, definition.Expression, addPredecessor); err != nil {
			return tableDumpEdges{}, err
		}
		for _, dependency := range definition.Dependencies {
			if err := addPredecessor(dependency.Address); err != nil {
				return tableDumpEdges{}, err
			}
		}
	}
	for _, definition := range r.ColumnMeta.Generations {
		if c.recipePlan[r.Address()] != nil {
			continue
		}
		for _, dependency := range definition.Dependencies {
			predecessor, err := dumpPredecessor(c.catalogView, dependency.Address, r.Address())
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
