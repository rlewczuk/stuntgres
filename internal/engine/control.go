package engine

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"stuntgres/internal/parser"
)

func (e *Engine) Reset(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fresh := New()
	_, err := e.ReplaceCatalog(ctx, fresh.CatalogRead())
	return err
}
func quoteIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Namespace ACL entry order and split entries do not affect reproducibility.
// Principal/grantor references do: losing them would change DROP dependencies.
func bootstrapNamespaceDumpable(d *namespaceDescriptor) bool {
	if !d.acl.Explicit {
		return false
	}
	publicRights := UsagePrivilege
	if d.address.ObjectID == PublicNamespaceOID {
		publicRights |= CreatePrivilege
	}
	var publicUse, publicOptions, ownerUse, ownerOptions Privileges
	for _, entry := range d.acl.Entries {
		if entry.Grantor != d.owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (PrincipalRef{}) {
				return false
			}
			publicUse |= entry.Privileges
			publicOptions |= entry.GrantOptions
		} else {
			if entry.Grantee.Role != d.owner {
				return false
			}
			ownerUse |= entry.Privileges
			ownerOptions |= entry.GrantOptions
		}
	}
	return publicUse == publicRights && publicOptions == 0 &&
		ownerUse == UsagePrivilege|CreatePrivilege && ownerOptions == UsagePrivilege|CreatePrivilege
}
func (e *Engine) Dump(ctx context.Context, dst io.Writer) error {
	if dst == nil {
		return fmt.Errorf("stuntgres: nil dump writer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return err
	}
	var invalid *relationDescriptor
	var invalidSchema string
	for _, descriptor := range e.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		namespace := e.state.descriptors[index.namespace].(*namespaceDescriptor)
		if namespace.temporary != (SessionToken{}) || index.sqlIndex.ready && index.sqlIndex.valid && index.sqlIndex.live {
			continue
		}
		if invalid == nil || namespace.name < invalidSchema || namespace.name == invalidSchema && index.name < invalid.name {
			invalid, invalidSchema = index, namespace.name
		}
	}
	if invalid != nil {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: dump cannot serialize invalid index %s.%s", quoteIdentifier(invalidSchema), quoteIdentifier(invalid.name))
	}
	tables := make([]dumpRelation, 0)
	catalog := CatalogRead{owner: e, state: e.state}
	domainRoles := RoleRead{e, e.roles}
	if err := e.validateTemporaryCollationDump(ctx, catalog); err != nil {
		e.mu.Unlock()
		return err
	}
	var domains []*typeDescriptor
	var collations []*collationDescriptor
	type dumpSchema struct{ name, owner string }
	var schemas []dumpSchema
	var sequences []dumpSequence
	type dumpView struct {
		owner, sql string
	}
	var views []dumpView
	var materialized []dumpRelation
	var indexes []dumpView
	viewDependencies := make(map[OID][]OID)
	viewIndices := make(map[OID]int)
	tableIndices := make(map[OID]int)
	indexIndices := make(map[OID]int)
	composites := make(map[OID]*relationDescriptor)
	// Deferral removes only eligible definitions' edges. Inline consumers keep
	// their prerequisites, including providers shared with deferred defaults.
	var inlinePredecessors map[OID][]OID
	deferDefaults := false
	type dumpComment struct{ name, text string }
	var schemaComments []dumpComment
	// Reconstruct bootstrap identity changes before creating ordinary namespaces.
	// These statements run only in Restore's isolated catalog under bootstrap
	// authority; ACLs are reproduced by normal owner-transfer semantics.
	var preamble []string
	type builtinSchema struct {
		original, name, owner string
		moved                 bool
	}
	var builtins []builtinSchema
	for _, b := range []struct {
		oid  OID
		name string
	}{{PublicNamespaceOID, "public"}, {PGCatalogNamespaceOID, "pg_catalog"}, {13229, "information_schema"}} {
		d, exists := e.state.descriptors[ObjectAddress{ClassID: NamespaceClassID, ObjectID: b.oid}]
		if !exists {
			preamble = append(preamble, "DROP SCHEMA "+quoteIdentifier(b.name)+";\n")
			continue
		}
		ns := d.(*namespaceDescriptor)
		role, err := (RoleRead{e, e.roles}).Descriptor(ns.owner)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		builtins = append(builtins, builtinSchema{original: b.name, name: ns.name, owner: role.Name})
	}
	remaining := 0
	for i := range builtins {
		builtins[i].moved = builtins[i].name == builtins[i].original
		if !builtins[i].moved {
			remaining++
		}
	}
	for remaining > 0 {
		progress := false
		for i := range builtins {
			b := &builtins[i]
			if b.moved {
				continue
			}
			occupied := false
			for j := range builtins {
				if !builtins[j].moved && builtins[j].original == b.name {
					occupied = true
				}
			}
			if occupied {
				continue
			}
			preamble = append(preamble, "ALTER SCHEMA "+quoteIdentifier(b.original)+" RENAME TO "+quoteIdentifier(b.name)+";\n")
			b.moved = true
			remaining--
			progress = true
		}
		if progress {
			continue
		}
		// A cycle needs a free ordinary spelling, never a reserved pg_ name.
		for i := range builtins {
			b := &builtins[i]
			if b.moved {
				continue
			}
			var temporary string
			for suffix := 0; ; suffix++ {
				candidate := fmt.Sprintf("stuntgres_restore_namespace_%d_%d", i, suffix)
				if _, exists := e.state.names[NameKey{ClassID: NamespaceClassID, Name: candidate}]; !exists {
					temporary = candidate
					break
				}
			}
			preamble = append(preamble, "ALTER SCHEMA "+quoteIdentifier(b.original)+" RENAME TO "+quoteIdentifier(temporary)+";\n")
			b.original = temporary
			break
		}
	}
	for _, b := range builtins {
		if b.owner != "postgres" {
			preamble = append(preamble, "ALTER SCHEMA "+quoteIdentifier(b.name)+" OWNER TO "+quoteIdentifier(b.owner)+";\n")
		}
	}
	for a, d := range e.state.descriptors {
		if ns, ok := e.state.descriptors[d.Namespace()].(*namespaceDescriptor); ok && ns.temporary != (SessionToken{}) {
			continue
		}
		r, ok := d.(*relationDescriptor)
		if !ok {
			switch t := d.(type) {
			case *typeDescriptor:
				if t.typ.IsUserDefined() && !t.typ.IsComposite() {
					domains = append(domains, t)
				} else if t.typ.IsArray() {
					if err := (typeProvider{}).Validate(catalog, t); err != nil {
						e.mu.Unlock()
						return err
					}
				} else if t.Namespace().ObjectID != PGCatalogNamespaceOID && t.Namespace().ObjectID != 13229 && t.owningRelation == (ObjectAddress{}) {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: dump cannot serialize unsupported user type")
				}
				continue
			case *collationDescriptor:
				if err := (collationProvider{}).Validate(catalog, t); err != nil {
					e.mu.Unlock()
					return err
				}
				if !t.seeded() {
					collations = append(collations, t)
				}
				continue
			case *conversionDescriptor:
				continue
			}
			if d.Kind() == NamespaceObject {
				ns, valid := d.(*namespaceDescriptor)
				if valid && ns.temporary != (SessionToken{}) {
					continue
				}
				if valid && ns.comment != namespaceCommentBaseline(a.ObjectID) {
					schemaComments = append(schemaComments, dumpComment{ns.name, ns.comment})
				}
				if valid && (a.ObjectID == PublicNamespaceOID || a.ObjectID == PGCatalogNamespaceOID || a.ObjectID == 13229) &&
					!bootstrapNamespaceDumpable(ns) {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: dump cannot serialize changed builtin namespace ACL")
				}
				if valid && a.ObjectID != PublicNamespaceOID && a.ObjectID != PGCatalogNamespaceOID && a.ObjectID != 13229 {
					if ns.acl.Explicit {
						e.mu.Unlock()
						return fmt.Errorf("stuntgres: dump cannot serialize explicit namespace ACL")
					}
					role, err := (RoleRead{e, e.roles}).Descriptor(ns.owner)
					if err != nil {
						e.mu.Unlock()
						return err
					}
					schemas = append(schemas, dumpSchema{ns.name, role.Name})
				}
				continue
			}
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: unsupported catalog descriptor kind")
		}
		if r.relationKind == catalogRelation {
			continue
		}
		ns, ok := e.state.descriptors[r.namespace].(*namespaceDescriptor)
		if !ok {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: relation namespace is missing")
		}
		if ns.temporary != (SessionToken{}) {
			continue
		}
		if r.acl.Explicit || len(r.columnACL) != 0 {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: dump cannot serialize explicit relation or column ACL")
		}
		principal, err := (RoleRead{e, e.roles}).Descriptor(r.owner)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		if r.relationKind == compositeRelation {
			composites[r.address.ObjectID] = r
			viewDependencies[r.address.ObjectID] = nil
			continue
		}
		if r.sqlIndex != nil {
			if r.sqlIndex.constraint != nil {
				continue
			}
			sql, err := indexDefinitionSQL(ctx, catalog, r, indexPresentation{dump: true})
			if err != nil {
				e.mu.Unlock()
				return err
			}
			oid := r.address.ObjectID
			viewDependencies[oid] = []OID{r.sqlIndex.parent.ObjectID}
			for _, key := range r.sqlIndex.keys {
				if key.derived == nil {
					continue
				}
				for _, dependency := range key.derived.dependencies {
					predecessor, err := dumpPredecessor(catalog, dependency.Address, r.sqlIndex.parent)
					if err != nil {
						e.mu.Unlock()
						return err
					}
					if predecessor != 0 && !slices.Contains(viewDependencies[oid], predecessor) {
						viewDependencies[oid] = append(viewDependencies[oid], predecessor)
					}
				}
			}
			indexIndices[oid] = len(indexes)
			indexes = append(indexes, dumpView{principal.Name, sql})
			continue
		}
		if r.view != nil {
			// Materialized definitions preserve their analyzed presentation
			// without injecting synthetic source aliases during replay. A nil
			// path qualifies identities for portable, executable dump SQL.
			presentation := viewPresentation{columns: r.columns, deparse: r.relationKind == materializedViewRelation}
			body, err := r.view.selectSQL(ctx, CatalogRead{owner: e, state: e.state}, presentation)
			if err != nil {
				e.mu.Unlock()
				return err
			}
			var labels []string
			for _, column := range r.columns {
				labels = append(labels, quoteIdentifier(column.Name))
			}
			columns := ""
			if len(labels) > 0 {
				columns = " (" + strings.Join(labels, ", ") + ")"
			}
			sql := "CREATE VIEW " + quoteIdentifier(ns.name) + "." + quoteIdentifier(r.name) + columns +
				fmt.Sprintf(" WITH (security_barrier=%t, security_invoker=%t) AS ", r.view.barrier, r.view.invoker) + body + ";\n"
			if r.relationKind == materializedViewRelation {
				sql = "CREATE MATERIALIZED VIEW " + quoteIdentifier(ns.name) + "." + quoteIdentifier(r.name) + columns + " AS " + body + " WITH NO DATA;\n"
				storage := e.storage[a]
				if storage == nil {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: materialized view storage is missing")
				}
				materialized = append(materialized, dumpRelation{owner: principal.Name, schema: ns.name, name: r.name, columns: r.columns, attributes: r.attributes, versions: storage.versions, ends: storage.ends, frontier: e.commitSeq, descriptor: r})
			}
			// Capture every source occurrence and expression dependency from the
			// same immutable catalog used to render the definition. Attribute
			// references depend on their parent relation, not its column OID.
			predecessors := make(map[OID]bool)
			addPredecessor := func(address ObjectAddress) error {
				predecessor, err := dumpPredecessor(CatalogRead{owner: e, state: e.state}, address, r.address)
				if err != nil {
					return err
				}
				if predecessor != 0 {
					predecessors[predecessor] = true
				}
				return nil
			}
			for _, leaf := range r.view.sources {
				if err := addPredecessor(leaf.ref.Address); err != nil {
					e.mu.Unlock()
					return err
				}
			}
			for _, dependency := range r.view.dependencies {
				if err := addPredecessor(dependency.ref.Address); err != nil {
					e.mu.Unlock()
					return err
				}
			}
			for _, literal := range r.view.literals {
				if err := regclassReplayReferences(CatalogRead{owner: e, state: e.state}, literal, addPredecessor); err != nil {
					e.mu.Unlock()
					return err
				}
			}
			oid := r.address.ObjectID
			viewDependencies[oid] = make([]OID, 0, len(predecessors))
			for predecessor := range predecessors {
				viewDependencies[oid] = append(viewDependencies[oid], predecessor)
			}
			viewIndices[oid] = len(views)
			views = append(views, dumpView{principal.Name, sql})
			continue
		}
		if r.relationKind == sequenceRelation {
			g := e.sequences[a]
			if g == nil {
				e.mu.Unlock()
				return fmt.Errorf("stuntgres: sequence state missing")
			}
			sequence := dumpSequence{owner: principal.Name, schema: ns.name, name: r.name, options: *r.sequence, state: *g, comment: r.comment}
			if owned := r.sequence.owned; owned != (ObjectAddress{}) {
				tableAddress := owned
				tableAddress.SubID = 0
				table, ok := e.state.descriptors[tableAddress].(*relationDescriptor)
				if !ok {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: sequence ownership target missing")
				}
				tableSchema := e.state.descriptors[table.namespace].Name()
				sequence.ownedTable = quoteIdentifier(tableSchema) + "." + quoteIdentifier(table.name)
				for i, sub := range table.attributes {
					if sub == owned.SubID {
						sequence.ownedColumn = table.columns[i].Name
						if r.sequence.identity {
							sequence.divergent = identityDumpDivergent(*r.sequence, table.columns[i].Usage.Type.ID())
						}
					}
				}
			}
			sequences = append(sequences, sequence)
			continue
		}
		storage := e.storage[a]
		if storage == nil {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: relation storage is missing")
		}
		oid := r.address.ObjectID
		var predecessors []OID
		for _, check := range r.checkConstraints {
			if err := regclassReplayReferences(CatalogRead{owner: e, state: e.state}, check.expression, func(address ObjectAddress) error {
				predecessor, err := dumpPredecessor(CatalogRead{owner: e, state: e.state}, address, r.address)
				if err == nil && predecessor != 0 {
					predecessors = append(predecessors, predecessor)
				}
				return err
			}); err != nil {
				e.mu.Unlock()
				return err
			}
			for _, dependency := range check.dependencies {
				predecessor, err := dumpPredecessor(CatalogRead{owner: e, state: e.state}, dependency.ref.Address, r.address)
				if err != nil {
					e.mu.Unlock()
					return err
				}
				if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
					predecessors = append(predecessors, predecessor)
				}
			}
		}
		var deferredPredecessors []OID
		var deferredDefinitions map[int32]bool
		for attribute, definition := range r.defaults {
			eligible := defaultReplayDeferrable(definition)
			if eligible {
				if deferredDefinitions == nil {
					deferredDefinitions = make(map[int32]bool)
				}
				deferredDefinitions[attribute] = true
				deferDefaults = deferDefaults || definition.expression.ResultUsage().Type.ContainsRegclass()
			}
			addPredecessor := func(address ObjectAddress) error {
				owner := r.address
				if eligible {
					owner = ObjectAddress{}
				}
				predecessor, err := dumpPredecessor(catalog, address, owner)
				if err != nil {
					return err
				}
				if eligible {
					if !deferDefaults && predecessor != 0 && !slices.Contains(deferredPredecessors, predecessor) {
						deferredPredecessors = append(deferredPredecessors, predecessor)
					}
				} else if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
					predecessors = append(predecessors, predecessor)
				}
				return nil
			}
			// Opaque literal payloads need replay ordering even when their BOOL/
			// TEXT result is inline and intentionally has no relation DROP edge.
			if err := regclassReplayReferences(catalog, definition.expression, addPredecessor); err != nil {
				e.mu.Unlock()
				return err
			}
			for _, dependency := range definition.dependencies {
				if err := addPredecessor(dependency.Address); err != nil {
					e.mu.Unlock()
					return err
				}
			}
		}
		for _, definition := range r.generations {
			for _, dependency := range definition.dependencies {
				predecessor, err := dumpPredecessor(catalog, dependency.Address, r.address)
				if err != nil {
					e.mu.Unlock()
					return err
				}
				if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
					predecessors = append(predecessors, predecessor)
				}
			}
		}
		viewDependencies[oid] = predecessors
		if len(deferredPredecessors) != 0 && !deferDefaults {
			if inlinePredecessors == nil {
				inlinePredecessors = make(map[OID][]OID)
			}
			inlinePredecessors[oid] = predecessors
			combined := slices.Clone(predecessors)
			for _, predecessor := range deferredPredecessors {
				if !slices.Contains(combined, predecessor) {
					combined = append(combined, predecessor)
				}
			}
			viewDependencies[oid] = combined
		}
		tableIndices[oid] = len(tables)
		linked := make(map[ObjectAddress]dumpSequence, len(r.identities))
		for _, identity := range r.identities {
			sequence := e.state.descriptors[identity.sequence].(*relationDescriptor)
			linked[identity.sequence] = dumpSequence{owner: principal.Name, schema: ns.name, name: sequence.name, options: *sequence.sequence, state: *e.sequences[identity.sequence], comment: sequence.comment}
		}
		tables = append(tables, dumpRelation{owner: principal.Name, schema: ns.name, name: r.name, columns: r.columns, attributes: r.attributes, versions: storage.versions, ends: storage.ends, frontier: e.commitSeq, comment: r.comment, columnComments: r.columnComments, notNullConstraints: r.notNullConstraints, defaults: r.defaults, deferredDefinitions: deferredDefinitions, descriptor: r, identities: r.identities, identitySequences: linked})
	}
	e.mu.Unlock()
	domainNodes := make(map[OID]*typeDescriptor, len(domains))
	for _, d := range domains {
		id := d.Address().ObjectID
		domainNodes[id] = d
		viewDependencies[id] = nil
		if d.typ.IsDomain() {
			predecessor, err := dumpPredecessor(catalog, ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.typ.DomainBase().Type.ID())}, d.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				viewDependencies[id] = []OID{predecessor}
			}
		}
	}
	for id, predecessors := range viewDependencies {
		relation, ok := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: id}].(*relationDescriptor)
		if !ok {
			continue
		}
		columns, err := dumpColumnPredecessors(catalog, relation, predecessors)
		if err != nil {
			return err
		}
		viewDependencies[id] = columns
		if inline, ok := inlinePredecessors[id]; ok {
			inline, err = dumpColumnPredecessors(catalog, relation, inline)
			if err != nil {
				return err
			}
			inlinePredecessors[id] = inline
		}
	}
	collationStatements, err := collationDumpStatements(ctx, catalog, domainRoles, collations)
	if err != nil {
		return err
	}
	seedStatements, err := collationSeedDumpStatements(ctx, catalog, domainRoles)
	if err != nil {
		return err
	}
	if deferDefaults {
		for oid, predecessors := range inlinePredecessors {
			viewDependencies[oid] = predecessors
		}
	}
	viewOrder, err := viewDumpOrder(ctx, viewDependencies)
	if err != nil && !deferDefaults && ctx.Err() == nil && len(inlinePredecessors) != 0 {
		for oid, predecessors := range inlinePredecessors {
			viewDependencies[oid] = predecessors
		}
		viewOrder, err = viewDumpOrder(ctx, viewDependencies)
		deferDefaults = err == nil
	}
	if err != nil {
		return err
	}
	declarations, err := mixedDumpStatements(ctx, catalog, domainRoles, viewOrder, domainNodes, composites)
	if err != nil {
		return err
	}
	emit := func(s string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.WriteString(dst, s)
		if err != nil {
			return err
		}
		if n != len(s) {
			return io.ErrShortWrite
		}
		return ctx.Err()
	}
	for _, line := range preamble {
		if err := emit(line); err != nil {
			return err
		}
	}
	sort.Slice(schemas, func(i, j int) bool { return schemas[i].name < schemas[j].name })
	for _, s := range schemas {
		if err := emit("CREATE SCHEMA " + quoteIdentifier(s.name) + " AUTHORIZATION " + quoteIdentifier(s.owner) + ";\n"); err != nil {
			return err
		}
	}
	for _, line := range seedStatements {
		if err := emit(line); err != nil {
			return err
		}
	}
	sort.Slice(schemaComments, func(i, j int) bool { return schemaComments[i].name < schemaComments[j].name })
	for _, c := range schemaComments {
		literal := "NULL"
		if c.text != "" {
			literal = commentLiteral(c.text)
		}
		if err := emit("COMMENT ON SCHEMA " + quoteIdentifier(c.name) + " IS " + literal + ";\n"); err != nil {
			return err
		}
	}
	sequenceCreation, sequenceCompletion := sequenceDumpStatements(sequences)
	for _, line := range sequenceCreation {
		if err := emit(line); err != nil {
			return err
		}
	}
	currentOwner := ""
	for _, declaration := range collationStatements {
		if declaration.owner != currentOwner {
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(declaration.owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = declaration.owner
		}
		if err := emit(declaration.sql); err != nil {
			return err
		}
	}
	// The interpreter need not be logged in as the native bootstrap owner.
	// Establish even the first group's owner before creating dependent objects.
	for _, oid := range viewOrder {
		if declaration, exists := declarations[oid]; exists {
			if declaration.owner != currentOwner {
				if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(declaration.owner, "'", "''") + "';\n"); err != nil {
					return err
				}
				currentOwner = declaration.owner
			}
			if err := emit(declaration.sql); err != nil {
				return err
			}
			continue
		}
		if position, isIndex := indexIndices[oid]; isIndex {
			index := indexes[position]
			if index.owner != currentOwner {
				if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(index.owner, "'", "''") + "';\n"); err != nil {
					return err
				}
				currentOwner = index.owner
			}
			if err := emit(index.sql); err != nil {
				return err
			}
			continue
		}
		if index, isView := viewIndices[oid]; isView {
			view := views[index]
			if view.owner != currentOwner {
				if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(view.owner, "'", "''") + "';\n"); err != nil {
					return err
				}
				currentOwner = view.owner
			}
			if err := emit(view.sql); err != nil {
				return err
			}
			continue
		}
		t := tables[tableIndices[oid]]
		if t.owner != currentOwner {
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(t.owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = t.owner
		}
		qualified := quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name)
		var line strings.Builder
		line.WriteString("CREATE TABLE " + qualified + " (")
		for i, c := range t.columns {
			if i > 0 {
				line.WriteString(", ")
			}
			declaration, err := catalogTypeDeclaration(catalog, c.Usage)
			if err != nil {
				return err
			}
			line.WriteString(quoteIdentifier(c.Name) + " " + declaration)
			if c.Usage.Collation.ID != c.Usage.Type.Usage().Collation.ID {
				name := collationSQLName(catalog, c.Usage.Collation.ID)
				if name == "" {
					return fmt.Errorf("stuntgres: unsupported stored collation")
				}
				line.WriteString(" COLLATE " + name)
			}
			if definition, exists := t.defaults[t.attributes[i]]; exists && !(deferDefaults && t.deferredDefinitions[t.attributes[i]]) {
				text, err := defaultSQL(ctx, catalog, definition.expression)
				if err != nil {
					return err
				}
				line.WriteString(" DEFAULT " + text)
			}
			if definition, exists := t.descriptor.generations[t.attributes[i]]; exists {
				text, err := (scalarRenderer{owner: t.descriptor}).sql(ctx, catalog, definition.expression)
				if err != nil {
					return err
				}
				line.WriteString(" GENERATED ALWAYS AS (" + text + ") STORED")
			}
			if identity, exists := t.identities[t.attributes[i]]; exists {
				mode := "ALWAYS"
				if identity.mode == parser.IdentityByDefault {
					mode = "BY DEFAULT"
				}
				sequence := t.identitySequences[identity.sequence]
				line.WriteString(" GENERATED " + mode + " AS IDENTITY (SEQUENCE NAME " + quoteIdentifier(sequence.schema) + "." + quoteIdentifier(sequence.name))
				if !identityDumpDivergent(sequence.options, c.Usage.Type.ID()) {
					line.WriteString(" " + sequenceNumericSQL(sequence.options))
				}
				line.WriteString(")")
			}
			if !c.Nullable {
				for _, constraint := range t.notNullConstraints {
					if constraint.Attribute == t.attributes[i] {
						line.WriteString(" CONSTRAINT " + quoteIdentifier(constraint.Name) + " NOT NULL")
						break
					}
				}
			}
		}
		for _, c := range orderedChecks(t.descriptor) {
			text, err := (scalarRenderer{owner: t.descriptor, checkReplayPrecision: true}).sql(ctx, catalog, c.expression)
			if err != nil {
				return err
			}
			if len(t.columns) > 0 || line.Len() > len("CREATE TABLE "+qualified+" (") {
				line.WriteString(", ")
			}
			line.WriteString("CONSTRAINT " + quoteIdentifier(c.Name) + " CHECK (" + text + ")")
			if c.NoInherit {
				line.WriteString(" NO INHERIT")
			}
		}
		for _, c := range t.descriptor.uniqueConstraints {
			text, err := uniqueConstraintSQL(t.descriptor, &c)
			if err != nil {
				return err
			}
			line.WriteString(", CONSTRAINT " + quoteIdentifier(c.Name) + " " + text)
		}
		line.WriteString(");\n")
		if err := emit(line.String()); err != nil {
			return err
		}
		if t.comment != "" {
			if err := emit("COMMENT ON TABLE " + qualified + " IS " + commentLiteral(t.comment) + ";\n"); err != nil {
				return err
			}
		}
		for i, c := range t.columns {
			if text := t.columnComments[t.attributes[i]]; text != "" {
				if err := emit("COMMENT ON COLUMN " + qualified + "." + quoteIdentifier(c.Name) + " IS " + commentLiteral(text) + ";\n"); err != nil {
					return err
				}
			}
		}
	}
	if deferDefaults {
		for _, oid := range viewOrder {
			index, isTable := tableIndices[oid]
			if !isTable {
				continue
			}
			t := tables[index]
			if t.owner != currentOwner {
				if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(t.owner, "'", "''") + "';\n"); err != nil {
					return err
				}
				currentOwner = t.owner
			}
			if err := t.emitDeferredDefaults(ctx, catalog, emit); err != nil {
				return err
			}
		}
	}
	for _, oid := range viewOrder {
		index, isTable := tableIndices[oid]
		if !isTable {
			continue
		}
		t := tables[index]
		if t.owner != currentOwner {
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(t.owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = t.owner
		}
		if err := t.emitRows(ctx, catalog, emit); err != nil {
			return err
		}
	}
	for _, line := range sequenceCompletion {
		if err := emit(line); err != nil {
			return err
		}
	}
	slices.SortFunc(materialized, func(a, b dumpRelation) int {
		if order := strings.Compare(a.schema, b.schema); order != 0 {
			return order
		}
		return strings.Compare(a.name, b.name)
	})
	for i := range materialized {
		if err := materialized[i].emitMaterializedSnapshot(ctx, catalog, emit); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}
func (e *Engine) Restore(ctx context.Context, src io.Reader) error {
	if src == nil {
		return fmt.Errorf("stuntgres: nil restore reader")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stage := e.restoreStage()
	data, err := io.ReadAll(contextReader{ctx, src})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	var snapshots []materializedSnapshot
	statements, err := parser.ParseWithOptions(string(data), parser.Options{Context: ctx, StandardConformingStrings: true, LineComment: func(comment string) error {
		record, recognized, err := decodeMaterializedSnapshot(comment)
		if err == nil && recognized {
			snapshots = append(snapshots, record)
		}
		return err
	}})
	if err != nil {
		return err
	}
	// The isolated role snapshot retains exact target identities for final remapping.
	session, err := stage.StartSession(ctx, SessionStart{ID: 1, Authenticated: PrincipalRef{stage, 10}})
	if err != nil {
		return err
	}
	defer func() { session.RequestEnd(CleanClose); _ = session.WaitEnd(context.Background()) }()
	op, err := session.Acquire(ctx)
	if err != nil {
		return err
	}
	defer op.Done()
	for _, stmt := range statements {
		switch s := stmt.(type) {
		case *parser.CreateTable:
			if s.Persistence == "temporary" || len(s.Name.Parts) == 2 && (s.Name.Parts[0].Text == "pg_temp" || strings.HasPrefix(s.Name.Parts[0].Text, "pg_temp_")) {
				return sqlError("0A000", "restore cannot stage non-retractable effects")
			}
			for _, element := range s.Elements {
				if element.Column != nil {
					for _, constraint := range element.Column.Constraints {
						if err := restoreScalar(constraint.Default, true); err != nil {
							return err
						}
						if constraint.Generation != nil {
							if err := restoreScalar(constraint.Generation.Expression, true); err != nil {
								return err
							}
						}
						if constraint.Check != nil {
							if err := restoreScalar(constraint.Check.Expression, true); err != nil {
								return err
							}
						}
					}
				}
				if element.Check != nil {
					if err := restoreScalar(element.Check.Expression, true); err != nil {
						return err
					}
				}
			}
		case *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.CreateEnum, *parser.CreateDomain, *parser.CreateCollation, *parser.AlterCollation:
		case *parser.CreateView:
		case *parser.CreateQueryTable:
			if s.Form != parser.CreateMaterializedView || !s.NoData {
				return sqlError("0A000", "restore accepts only unpopulated materialized view definitions")
			}
		case *parser.CreateIndex:
		case *parser.CreateSequence:
			if s.Persistence == "temporary" {
				return sqlError("0A000", "restore cannot stage non-retractable effects")
			}
		case *parser.AlterSequence:
			if !(s.Action == "" && len(s.Options) == 1 && s.Options[0].Name == "owned") {
				if err := stage.validateRestoreIdentityConfiguration(op, s); err != nil {
					return err
				}
			}
		case *parser.AlterTableDefault:
			if s.Action != parser.SetDefault || s.Expression == nil {
				return sqlError("0A000", "restore accepts only deferred SET DEFAULT alterations")
			}
		case *parser.Select:
			if !restoreSequenceState(s) {
				return sqlError("0A000", "restore accepts only generated sequence state")
			}
		case *parser.Comment:
			if s.Kind == parser.CommentRole {
				return sqlError("0A000", "restore accepts only database-local COMMENT targets")
			}
		case *parser.SessionAuthorization:
			if !s.OwnerDirective {
				return sqlError("0A000", "restore accepts only named SET SESSION AUTHORIZATION owner directives")
			}
		case *parser.Insert:
			if s.Conflict != nil {
				return sqlError("0A000", "restore INSERT conflict clauses are unsupported")
			}
			if s.Source != nil {
				return sqlError("0A000", "restore INSERT query sources are unsupported")
			}
			for _, target := range s.Targets {
				for _, bound := range target.Subscripts {
					if bound.Lower != nil {
						if err := restoreScalar(bound.Lower, false); err != nil {
							return err
						}
					}
					if bound.Upper != nil {
						if err := restoreScalar(bound.Upper, false); err != nil {
							return err
						}
					}
				}
			}
			for _, row := range s.Rows {
				for _, v := range row {
					if err := restoreScalar(v, false); err != nil {
						return err
					}
				}
			}
		default:
			return sqlError("0A000", "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION")
		}
		op.restoreTableDestination = false
		if _, ok := stmt.(*parser.CreateTable); ok {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.CreateSequence); ok {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.CreateView); ok {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.CreateIndex); ok {
			op.restoreTableDestination = true
		}
		if creation, ok := stmt.(*parser.CreateQueryTable); ok && creation.Form == parser.CreateMaterializedView {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.CreateCollation); ok {
			op.restoreTableDestination = true
		}
		op.restoreInsertAuthority = false
		if _, ok := stmt.(*parser.Insert); ok {
			op.restoreInsertAuthority = true
		}
		op.restoreCommentAuthority = false
		if _, ok := stmt.(*parser.Comment); ok {
			op.restoreCommentAuthority = true
		}
		var result Result
		result, err = stage.Execute(op, stmt, 0, nil)
		if result.Rows != nil {
			closeErr := result.Rows.Close()
			if err == nil {
				err = closeErr
			}
		}
		op.restoreCommentAuthority = false
		op.DetachNotices()
		if err != nil {
			return err
		}
		if alteration, ok := stmt.(*parser.AlterTableDefault); ok {
			if err := stage.validateRestoredDefault(op, alteration); err != nil {
				return err
			}
		}
	}
	if err := stage.importMaterializedSnapshots(ctx, op, snapshots); err != nil {
		return err
	}
	_, err = e.replaceCatalog(ctx, stage.CatalogRead(), op)
	return err
}

// Explicit escape strings are independent of the destination string setting.
func commentLiteral(text string) string {
	return "E'" + strings.ReplaceAll(strings.ReplaceAll(text, "\\", "\\\\"), "'", "''") + "'"
}

// Stored CREATE expressions retain their binder's admission rules. INSERT
// expressions retain the existing Restore whitelist. Both modes reject every
// parameter reached through the shared recursive visitor.
func restoreScalar(raw parser.Expr, stored bool) error {
	switch r := raw.(type) {
	case parser.FunctionExpr:
		if !stored {
			if len(r.Name.Parts) < 1 || len(r.Name.Parts) > 2 {
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
			switch r.Name.Parts[len(r.Name.Parts)-1].Text {
			case "btrim", "ltrim", "rtrim", "normalize", "is_normalized", "lower", "upper", "casefold", "initcap", "bit_length", "char_length", "character_length", "length", "octet_length", "position", "strpos", "overlay", "replace", "translate", "starts_with", "lpad", "rpad", "repeat", "reverse", "ascii", "chr", "to_ascii", "unistr", "unicode_assigned":
			case "to_bin", "to_oct", "to_hex":
			case "pg_size_bytes", "pg_size_pretty":
			case "age", "isfinite":
			case "overlaps":
			case "date_trunc":
			case "timezone", "date_add", "date_subtract":
			case "justify_days", "justify_hours", "justify_interval":
			case "make_date", "make_time", "make_timestamp", "make_timestamptz", "make_interval":
			case "gen_random_uuid", "uuidv4", "uuidv7":
			case "format":
			case "convert", "convert_from", "convert_to", "encode", "decode", "crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512", "bit_count", "get_bit", "get_byte", "set_bit", "set_byte":
			case "like_escape":
				if len(r.Name.Parts) != 2 || r.Name.Parts[0].Text != "pg_catalog" || len(r.Args) != 2 || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			case "similar_to_escape":
				if r.Syntax != parser.SimilarEscapeFunctionSyntax || len(r.Name.Parts) != 2 || r.Name.Parts[0].Text != "pg_catalog" || len(r.Args) < 1 || len(r.Args) > 2 || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			case "substring":
				if len(r.Args) != 3 || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			default:
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
		}
		for _, child := range r.Args {
			if err := restoreScalar(child, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.CaseExpr:
		if r.Test != nil {
			if err := restoreScalar(r.Test, stored); err != nil {
				return err
			}
		}
		for _, arm := range r.Arms {
			if err := restoreScalar(arm.Condition, stored); err != nil {
				return err
			}
			if err := restoreScalar(arm.Result, stored); err != nil {
				return err
			}
		}
		if r.Else != nil {
			return restoreScalar(r.Else, stored)
		}
		return nil
	case parser.ConditionalExpr:
		for _, child := range r.Args {
			if err := restoreScalar(child, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.LogicalExpr:
		if err := restoreScalar(r.Left, stored); err != nil {
			return err
		}
		if r.Right != nil {
			return restoreScalar(r.Right, stored)
		}
		return nil
	case parser.PredicateExpr:
		if err := restoreScalar(r.Left, stored); err != nil {
			return err
		}
		if r.Right != nil {
			return restoreScalar(r.Right, stored)
		}
		return nil
	case parser.CollateExpr:
		return restoreScalar(r.Child, stored)
	case parser.CastExpr:
		return restoreScalar(r.Child, stored)
	case parser.RegclassExpr:
		return restoreScalar(r.Child, stored)
	case parser.ArrayExpr:
		for _, element := range r.Elements {
			if err := restoreScalar(element, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.SubscriptExpr:
		var storage [13]parser.Expr
		for _, child := range r.Children(&storage) {
			if err := restoreScalar(child, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.TimeZoneExpr:
		if err := restoreScalar(r.Value, stored); err != nil {
			return err
		}
		if !r.Local {
			return restoreScalar(r.Zone, stored)
		}
		return nil
	case parser.ParameterExpr:
		return expressionError("42P02", "parameters are not allowed in restore", r.Pos)
	case parser.CurrentDateTimeExpr, parser.IdentityExpr, parser.LiteralExpr, parser.TypedLiteralExpr, parser.DefaultExpr:
		return nil
	case parser.OperatorExpr:
		if !stored {
			if len(r.Name.Parts) < 1 || len(r.Name.Parts) > 2 {
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
			spelling := r.Name.Parts[len(r.Name.Parts)-1].Text
			if spelling == "!~" || spelling == "~" && len(r.Args) == 2 {
				if !r.SQLSimilar || len(r.Args) != 2 {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
				normalizer, ok := r.Args[1].(parser.FunctionExpr)
				if !ok || normalizer.Syntax != parser.SimilarEscapeFunctionSyntax {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			} else if r.SQLSimilar {
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
		}
		for _, child := range r.Args {
			if err := restoreScalar(child, stored); err != nil {
				return err
			}
		}
		return nil
	default:
		if stored {
			return nil
		} // Existing stored binder owns unsupported syntax.
		return fmt.Errorf("stuntgres: invalid restore scalar")
	}
}
