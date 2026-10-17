package engine

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

func (e *Engine) Reset(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fresh := NewWithSettings(nil, e.databaseName, false)
	if err := fresh.InstallForeignDefinitions(e.foreignWrappers, e.foreignValidators, e.foreignValueImporter, e.foreignValueExporter); err != nil {
		return err
	}
	_, err := e.ReplaceCatalog(ctx, fresh.CatalogRead())
	return err
}
func quoteIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Namespace ACL entry order and split entries do not affect reproducibility.
// Principal/grantor references do: losing them would change DROP dependencies.
func bootstrapNamespaceDumpable(d *namespaceDescriptor) bool {
	if d.address.ObjectID == PGToastNamespaceOID {
		return !d.acl.Explicit && len(d.acl.Entries) == 0
	}
	if !d.acl.Explicit {
		return false
	}
	publicRights := authorization.UsagePrivilege
	var publicUse, publicOptions, ownerUse, ownerOptions authorization.Privileges
	for _, entry := range d.acl.Entries {
		if entry.Grantor != d.owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (authorization.PrincipalRef{}) {
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
		ownerUse == authorization.UsagePrivilege|authorization.CreatePrivilege && ownerOptions == 0
}

// Dump emits replayable logical definitions and rows, including renamed,
// manual, partial and nested partitioned key/index trees. Virtual indexes have
// no physical roots; their actual ancestry and incomplete validity are retained.
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
	for _, descriptor := range e.state.descriptors {
		_, foreign := descriptor.(*foreignDescriptor)
		if relation, ok := descriptor.(*relationDescriptor); ok && relation.foreign != nil {
			foreign = true
		}
		if foreign {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: dump cannot serialize SQL/MED foreign metadata")
		}
	}
	if len(e.state.defaultACLs) != 0 {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: dump cannot serialize default privileges")
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
		parent, ok := e.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if ok && parent.partition != nil && index.sqlIndex.ready && index.sqlIndex.live {
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
	originalCatalog := catalog
	originalRoles := e.roleRead(originalCatalog, e.roles)
	recipePlan, err := capturedGenerationReplayPlan(originalCatalog)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	aclOutput := capturedACLItemOutput{owner: e, roles: originalRoles, lossless: true}
	ctx = aclItemOutputContext(ctx, aclOutput)
	if err := e.preflightACLItemDump(ctx, originalCatalog, aclOutput); err != nil {
		e.mu.Unlock()
		return err
	}
	// Declarations bind private key names until the terminal renames. Later
	// definitions and values must render against the original captured names.
	catalog, constraintReplay := partitionConstraintDumpCatalog(catalog)
	domainRoles := e.roleRead(catalog, e.roles)
	if err := e.validateTemporaryCollationDump(ctx, catalog); err != nil {
		e.mu.Unlock()
		return err
	}
	var domains []*typeDescriptor
	var collations []*collationDescriptor
	var conversions []*conversionDescriptor
	type dumpSchema struct {
		name, owner string
		configured  bool
	}
	var schemas []dumpSchema
	var sequences []dumpSequence
	type dumpView struct {
		owner, sql string
	}
	var views []dumpView
	var materialized []dumpRelation
	var indexes []dumpView
	viewDependencies := make(map[dumpEventKey][]dumpEventKey)
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
	for address, name := range e.foreignNamespaces {
		ns, ok := e.state.descriptors[address].(*namespaceDescriptor)
		if !ok || ns.name != name {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: dump cannot serialize a renamed or retired configured foreign namespace")
		}
	}
	type builtinSchema struct {
		original, name, owner, baselineOwner string
		moved                                bool
	}
	var builtins []builtinSchema
	for _, b := range []struct {
		oid  OID
		name string
	}{{PublicNamespaceOID, "public"}, {PGCatalogNamespaceOID, "pg_catalog"}, {PGToastNamespaceOID, "pg_toast"}, {13229, "information_schema"}} {
		d, exists := e.state.descriptors[ObjectAddress{ClassID: NamespaceClassID, ObjectID: b.oid}]
		if !exists {
			preamble = append(preamble, "DROP SCHEMA "+quoteIdentifier(b.name)+";\n")
			continue
		}
		ns := d.(*namespaceDescriptor)
		role, err := domainRoles.Descriptor(ns.owner)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		baselineOwner := "postgres"
		if b.oid == PublicNamespaceOID {
			baselineOwner = "pg_database_owner"
		}
		builtins = append(builtins, builtinSchema{original: b.name, name: ns.name, owner: role.Name, baselineOwner: baselineOwner})
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
		if b.owner != b.baselineOwner {
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
			case *databaseDescriptor:
				continue // Destination attachment authority is never serialized.
			case *parameterDescriptor:
				e.mu.Unlock()
				return fmt.Errorf("stuntgres: dump cannot serialize parameter ACL")
			case *tablespaceDescriptor:
				if err := (tablespaceProvider{}).Validate(catalog, t); err != nil {
					e.mu.Unlock()
					return err
				}
				if t.owner != e.authorizationDomain.Principal(authorization.BootstrapPrincipalID) || t.acl.Explicit {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: dump cannot serialize altered tablespace authority")
				}
				continue
			case *operatorDescriptor:
				if !pgDependPinned(catalog, t.address) {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: dump cannot serialize custom operator identity")
				}
				continue
			case *procedureDescriptor:
				if t.foreign != nil {
					if err := (extensionCatalogProvider{ProcedureClassID}).Validate(catalog, t); err != nil {
						e.mu.Unlock()
						return err
					}
					if t.acl.Explicit || t.owner != e.authorizationDomain.Principal(10) {
						e.mu.Unlock()
						return fmt.Errorf("stuntgres: dump cannot serialize altered native foreign capability authority")
					}
					continue
				}
				if t.builtin != nil {
					if err := (extensionCatalogProvider{ProcedureClassID}).Validate(catalog, t); err != nil {
						e.mu.Unlock()
						return err
					}
					if t.owner != e.authorizationDomain.Principal(authorization.BootstrapPrincipalID) || t.acl.Explicit {
						e.mu.Unlock()
						return fmt.Errorf("stuntgres: dump cannot serialize altered builtin routine authority")
					}
					continue
				}
				if t.conversion.Name() != "" {
					if err := (extensionCatalogProvider{ProcedureClassID}).Validate(catalog, t); err != nil {
						e.mu.Unlock()
						return err
					}
					if t.owner != e.authorizationDomain.Principal(10) || t.acl.Explicit {
						e.mu.Unlock()
						return fmt.Errorf("stuntgres: dump cannot serialize altered builtin conversion routine authority")
					}
					continue
				}
				if t.acl.Explicit {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: dump cannot serialize explicit routine ACL")
				}
				if extensionMembership(t) == (ObjectAddress{}) {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: dump cannot serialize loose extension member")
				}
				continue
			case *tsTemplateDescriptor, *tsDictionaryDescriptor:
				if extensionMembership(t) == (ObjectAddress{}) {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: dump cannot serialize loose extension member")
				}
				continue
			case *extensionDescriptor:
				if err := (extensionCatalogProvider{ExtensionClassID}).Validate(catalog, t); err != nil {
					e.mu.Unlock()
					return err
				}
				ns := catalog.state.descriptors[t.targetNamespace].(*namespaceDescriptor)
				if ns.temporary != (SessionToken{}) {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: temporary extension installation is not portable")
				}
				principal, err := domainRoles.Descriptor(t.owner)
				if err != nil {
					e.mu.Unlock()
					return err
				}
				metadata, err := extensionOwnerMetadata(catalog, domainRoles, t)
				if err != nil {
					e.mu.Unlock()
					return err
				}
				text := "CREATE EXTENSION " + quoteIdentifier(t.name) + " WITH SCHEMA " + quoteIdentifier(ns.name) + " VERSION '" + strings.ReplaceAll(t.version, "'", "''") + "';\n" + metadata
				viewDependencies[dumpEventKey{oid: t.address.ObjectID}] = nil
				viewIndices[t.address.ObjectID] = len(views)
				views = append(views, dumpView{principal.Name, text})
				continue
			case *typeDescriptor:
				// A recipe can regenerate synthetic type defaults, never a real
				// explicit ACL (even one equal to the effective baseline).
				if t.acl.Explicit && !defaultDomainACL(t) {
					message := "stuntgres: dump cannot serialize changed type ACL"
					if t.typ.IsDomain() || t.typ.IsEnum() {
						message = "stuntgres: dump cannot serialize changed domain ACL"
					} else if t.typ.IsComposite() {
						message = "stuntgres: dump cannot serialize changed composite ACL"
					}
					e.mu.Unlock()
					return fmt.Errorf("%s", message)
				}
				if t.typ.IsQueryInt() || t.extension != (ObjectAddress{}) {
					if err := (typeProvider{}).Validate(catalog, t); err != nil {
						e.mu.Unlock()
						return err
					}
					continue
				}
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
				if err := (conversionProvider{}).Validate(catalog, t); err != nil {
					e.mu.Unlock()
					return err
				}
				if !t.seeded() {
					conversions = append(conversions, t)
				}
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
				if valid && (a.ObjectID == PublicNamespaceOID || a.ObjectID == PGCatalogNamespaceOID || a.ObjectID == PGToastNamespaceOID || a.ObjectID == 13229) &&
					!bootstrapNamespaceDumpable(ns) {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: dump cannot serialize changed builtin namespace ACL")
				}
				if valid && a.ObjectID != PublicNamespaceOID && a.ObjectID != PGCatalogNamespaceOID && a.ObjectID != PGToastNamespaceOID && a.ObjectID != 13229 {
					if ns.acl.Explicit {
						e.mu.Unlock()
						return fmt.Errorf("stuntgres: dump cannot serialize explicit namespace ACL")
					}
					role, err := domainRoles.Descriptor(ns.owner)
					if err != nil {
						e.mu.Unlock()
						return err
					}
					_, configured := e.foreignNamespaces[a]
					schemas = append(schemas, dumpSchema{name: ns.name, owner: role.Name, configured: configured})
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
		principal, err := domainRoles.Descriptor(r.owner)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		if r.relationKind == compositeRelation {
			composites[r.address.ObjectID] = r
			viewDependencies[dumpEventKey{oid: r.address.ObjectID}] = nil
			continue
		}
		if r.sqlIndex != nil {
			// Key declarations and their table constraints must use the same
			// captured private-name projection, including deferred local keys.
			r = catalog.state.descriptors[r.address].(*relationDescriptor)
			virtual := partitionReplayVirtualUniqueRoot(catalog, r)
			if virtual != nil && virtual.address != r.address {
				continue
			}
			if r.sqlIndex.constraint != nil && !partitionReplaySeparateConstraint(catalog, r) {
				continue
			}
			var sql string
			var err error
			if virtual != nil {
				sql, err = partitionVirtualUniqueDefinitionSQL(catalog, r)
			} else {
				sql, err = indexDefinitionSQL(ctx, catalog, r, indexPresentation{dump: true})
			}
			if err != nil {
				e.mu.Unlock()
				return err
			}
			oid := r.address.ObjectID
			if partitionReplayUsingIndex(catalog, r) {
				parent := catalog.state.descriptors[r.sqlIndex.parent].(*relationDescriptor)
				kind := "UNIQUE"
				if r.sqlIndex.constraint.Primary {
					kind = "PRIMARY KEY"
				}
				sql += "ALTER TABLE " + quoteIdentifier(ns.name) + "." + quoteIdentifier(parent.name) + " ADD CONSTRAINT " + quoteIdentifier(r.name) + " " + kind + " USING INDEX " + quoteIdentifier(r.name) + ";\n"
			}
			event := dumpEventKey{oid: oid}
			viewDependencies[event] = []dumpEventKey{{oid: r.sqlIndex.parent.ObjectID}}
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
					source := dumpEventKey{oid: predecessor}
					if predecessor != 0 && !slices.Contains(viewDependencies[event], source) {
						viewDependencies[event] = append(viewDependencies[event], source)
					}
				}
			}
			indexIndices[oid] = len(indexes)
			indexes = append(indexes, dumpView{principal.Name, sql})
			continue
		}
		if r.view != nil {
			// Materialized definitions preserve their analyzed presentation
			// without synthetic source aliases. Their explicit empty visibility
			// path below qualifies user types for portable, executable dump SQL.
			presentation := viewPresentation{columns: r.columns, deparse: r.relationKind == materializedViewRelation}
			if presentation.deparse {
				// A nil path is diagnostic naming's unqualified mode. Dump's
				// canonical materialized renderer must qualify user types too.
				presentation.path = []ObjectAddress{}
			}
			body, err := r.view.selectSQL(ctx, catalog, presentation)
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
			options := ""
			if len(r.view.options) != 0 {
				installed := make([]string, len(r.view.options))
				for i, option := range r.view.options {
					name, value, _ := strings.Cut(option, "=")
					installed[i] = quoteIdentifier(name) + "=" + commentLiteral(value)
				}
				options = " WITH (" + strings.Join(installed, ", ") + ")"
			}
			sql := "CREATE VIEW " + quoteIdentifier(ns.name) + "." + quoteIdentifier(r.name) + columns +
				options + " AS " + body + ";\n"
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
			for _, address := range r.autoExtensions {
				extension, ok := e.state.descriptors[address].(*extensionDescriptor)
				if !ok {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: missing materialized extension dump provider")
				}
				if err := addPredecessor(address); err != nil {
					e.mu.Unlock()
					return err
				}
				sql += "ALTER MATERIALIZED VIEW " + quoteIdentifier(ns.name) + "." + quoteIdentifier(r.name) + " DEPENDS ON EXTENSION " + quoteIdentifier(extension.name) + ";\n"
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
			event := dumpEventKey{oid: oid}
			viewDependencies[event] = make([]dumpEventKey, 0, len(predecessors))
			for predecessor := range predecessors {
				viewDependencies[event] = append(viewDependencies[event], dumpEventKey{oid: predecessor})
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
		if storage == nil && r.partition == nil {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: relation storage is missing")
		}
		oid := r.address.ObjectID
		var predecessors []OID
		if r.inheritance != nil {
			for _, parent := range r.inheritance.parents {
				predecessor, err := dumpPredecessor(catalog, parent, r.address)
				if err != nil {
					e.mu.Unlock()
					return err
				}
				predecessors = append(predecessors, predecessor)
			}
		}
		predecessors, err = partitionDumpPredecessors(catalog, r, predecessors)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		for _, check := range r.checkConstraints {
			if check.NotValid || recipePlan[r.address] != nil && recipePlan[r.address].deferredChecks[check.Name] {
				continue
			}
			add := func(address ObjectAddress) error {
				predecessor, err := dumpPredecessor(catalog, address, r.address)
				if err != nil {
					return err
				}
				if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
					predecessors = append(predecessors, predecessor)
				}
				return nil
			}
			if err := regclassReplayReferences(catalog, check.expression, add); err != nil {
				e.mu.Unlock()
				return err
			}
			for _, dependency := range check.dependencies {
				if err := add(dependency.ref.Address); err != nil {
					e.mu.Unlock()
					return err
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
			if recipePlan[r.address] != nil {
				continue
			}
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
		viewDependencies[dumpEventKey{oid: oid}] = dumpDeclarationEvents(predecessors)
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
			viewDependencies[dumpEventKey{oid: oid}] = dumpDeclarationEvents(combined)
		}
		tableIndices[oid] = len(tables)
		linked := make(map[ObjectAddress]dumpSequence, len(r.identities))
		for _, identity := range r.identities {
			sequence := e.state.descriptors[identity.sequence].(*relationDescriptor)
			linked[identity.sequence] = dumpSequence{owner: principal.Name, schema: ns.name, name: sequence.name, options: *sequence.sequence, state: *e.sequences[identity.sequence], comment: sequence.comment}
		}
		var versions []rowVersion
		var ends *deletionRoot
		if storage != nil {
			versions, ends = storage.versions, storage.ends
		}
		tables = append(tables, dumpRelation{owner: principal.Name, schema: ns.name, name: r.name, columns: r.columns, attributes: r.attributes, versions: versions, ends: ends, frontier: e.commitSeq, comment: r.comment, columnComments: r.columnComments, notNullConstraints: r.notNullConstraints, defaults: r.defaults, deferredDefinitions: deferredDefinitions, descriptor: r, identities: r.identities, identitySequences: linked})
		if selected := recipePlan[r.address]; selected != nil {
			tables[len(tables)-1].recipePlaceholders = true
			tables[len(tables)-1].recipeComponent = selected.recipeComponent
			tables[len(tables)-1].deferredChecks = selected.deferredChecks
		}
	}
	e.mu.Unlock()
	policyGroups, err := policyDumpGroups(ctx, originalCatalog, originalRoles)
	if err != nil {
		return err
	}
	ruleGroups, err := ruleDumpGroups(ctx, originalCatalog, originalRoles)
	if err != nil {
		return err
	}
	securityGroups, err := rowSecurityDumpGroups(ctx, catalog, domainRoles)
	if err != nil {
		return err
	}
	domainNodes := make(map[OID]*typeDescriptor, len(domains))
	for _, d := range domains {
		id := d.Address().ObjectID
		domainNodes[id] = d
		viewDependencies[dumpEventKey{oid: id}] = nil
		if d.typ.IsDomain() {
			predecessor, err := dumpPredecessor(catalog, ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.typ.DomainBase().Type.ID())}, d.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				viewDependencies[dumpEventKey{oid: id}] = []dumpEventKey{{oid: predecessor}}
			}
			for _, check := range d.domainChecks {
				if check.NotValid {
					continue
				}
				for _, dependency := range check.dependencies {
					predecessor, err := dumpPredecessor(catalog, dependency.ref.Address, d.Address())
					if err != nil {
						return err
					}
					event := dumpEventKey{oid: id}
					dependencyEvent := dumpEventKey{oid: predecessor}
					if predecessor != 0 && !slices.Contains(viewDependencies[event], dependencyEvent) {
						viewDependencies[event] = append(viewDependencies[event], dependencyEvent)
					}
				}
			}
		}
	}
	for event, predecessors := range viewDependencies {
		id := event.oid
		relation, ok := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: id}].(*relationDescriptor)
		if !ok {
			continue
		}
		columns, err := dumpColumnPredecessors(catalog, relation, dumpDeclarationIDs(predecessors))
		if err != nil {
			return err
		}
		viewDependencies[event] = dumpDeclarationEvents(columns)
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
	conversionStatements, conversionSchemas, err := conversionDumpStatements(ctx, catalog, domainRoles, conversions)
	if err != nil {
		return err
	}
	conversionSeeds, err := conversionSeedDumpStatements(ctx, catalog, domainRoles)
	if err != nil {
		return err
	}
	if deferDefaults {
		for oid, predecessors := range inlinePredecessors {
			viewDependencies[dumpEventKey{oid: oid}] = dumpDeclarationEvents(predecessors)
		}
	}
	// Capture collision-remap provenance from the same immutable snapshot as
	// the graph, before any dump-private naming substitutions.
	var priorities map[OID]OID
	for address, priority := range originalCatalog.state.dumpPriorities {
		if _, emitted := viewDependencies[dumpEventKey{oid: address.ObjectID}]; emitted {
			if priorities == nil {
				priorities = make(map[OID]OID)
			}
			priorities[address.ObjectID] = priority
		}
	}
	if err := physicalNotNullDumpDependencies(originalCatalog, viewDependencies); err != nil {
		return err
	}
	declarationOrder, err := viewDumpOrder(ctx, viewDependencies, priorities)
	if err != nil && !deferDefaults && ctx.Err() == nil && len(inlinePredecessors) != 0 {
		for oid, predecessors := range inlinePredecessors {
			viewDependencies[dumpEventKey{oid: oid}] = dumpDeclarationEvents(predecessors)
		}
		if err := physicalNotNullDumpDependencies(originalCatalog, viewDependencies); err != nil {
			return err
		}
		declarationOrder, err = viewDumpOrder(ctx, viewDependencies, priorities)
		deferDefaults = err == nil
	}
	if err != nil {
		return err
	}
	for i := range tables {
		if err := tables[i].prepareInheritedRecipes(originalCatalog); err != nil {
			return err
		}
	}
	viewOrder := dumpDeclarationIDs(declarationOrder)
	if err := partitionDumpEvents(ctx, originalCatalog, viewDependencies, declarationOrder, tables, indexIndices, constraintReplay, deferDefaults); err != nil {
		return err
	}
	invalidReplay := partitionInvalidDumpEvents(originalCatalog, viewDependencies, indexIndices)
	if err := domainInvalidDumpEvents(ctx, catalog, viewDependencies, domainNodes); err != nil {
		return err
	}
	eventOrder, err := viewDumpOrder(ctx, viewDependencies, priorities)
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
		prefix := "CREATE SCHEMA "
		if s.configured {
			prefix += "IF NOT EXISTS "
		}
		if err := emit(prefix + quoteIdentifier(s.name) + " AUTHORIZATION " + quoteIdentifier(s.owner) + ";\n"); err != nil {
			return err
		}
		if s.configured && s.owner != "postgres" {
			if err := emit("ALTER SCHEMA " + quoteIdentifier(s.name) + " OWNER TO " + quoteIdentifier(s.owner) + ";\n"); err != nil {
				return err
			}
		}
	}
	for _, line := range conversionSchemas {
		if err := emit(line); err != nil {
			return err
		}
	}
	for _, line := range seedStatements {
		if err := emit(line); err != nil {
			return err
		}
	}
	for _, line := range conversionSeeds {
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
	for _, declarations := range [2][]dumpDomain{collationStatements, conversionStatements} {
		for _, declaration := range declarations {
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
	}
	// The interpreter need not be logged in as the native bootstrap owner.
	// Establish even the first group's owner before creating dependent objects.
	setOwner := func(owner string) error {
		if currentOwner == owner {
			return nil
		}
		if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(owner, "'", "''") + "';\n"); err != nil {
			return err
		}
		currentOwner = owner
		return nil
	}
	for _, event := range eventOrder {
		oid := event.oid
		if event.phase != dumpDeclaration {
			if event.phase == dumpDomainChecks {
				text, err := domainInvalidDumpStatement(ctx, catalog, domainNodes[oid])
				if err != nil {
					return err
				}
				// A domain may legally be owned by a role without USAGE on its
				// schema. Replay only this post-data declaration as the existing
				// bootstrap authority; do not transfer ownership or invent ACLs.
				if err := setOwner("postgres"); err != nil {
					return err
				}
				if err := emit(text); err != nil {
					return err
				}
				continue
			}
			if event.phase == dumpTableCompletion {
				continue
			}
			if event.phase == dumpInvalidIndexPreparation || event.phase == dumpInvalidIndexCompletion {
				if err := setOwner("postgres"); err != nil {
					return err
				}
				text, err := invalidReplay[oid].sql(ctx, originalCatalog, event.phase)
				if err != nil {
					return err
				}
				if err := emit(text); err != nil {
					return err
				}
				continue
			}
			if event.phase == dumpRename || event.phase == dumpIndexAttachment || event.phase == dumpTableAttachment {
				if err := setOwner("postgres"); err != nil {
					return err
				}
				descriptor := originalCatalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: oid}].(*relationDescriptor)
				var text string
				switch event.phase {
				case dumpRename:
					text = constraintReplay.finalRenameSQL(descriptor)
				case dumpIndexAttachment:
					text = partitionIndexDumpAttachmentSQL(originalCatalog, descriptor)
				case dumpTableAttachment:
					text, err = partitionAttachDumpSQL(ctx, originalCatalog, descriptor)
					if err != nil {
						return err
					}
					text += constraintReplay.membershipRenames(catalog, descriptor, event)
				}
				if err := emit(text); err != nil {
					return err
				}
				continue
			}
			t := tables[tableIndices[oid]]
			owner := t.owner
			if event.phase == dumpInheritanceChecks || event.phase == dumpRetiredConstraints || event.phase == dumpInheritedForeignKeys || event.phase == dumpInvalidConstraints && inheritanceParticipant(originalCatalog, t.descriptor) {
				owner = "postgres"
			}
			if err := setOwner(owner); err != nil {
				return err
			}
			switch event.phase {
			case dumpDefaults:
				err = t.emitDeferredDefaults(ctx, originalCatalog, emit)
			case dumpRows:
				err = t.emitRows(ctx, originalCatalog, emit)
			case dumpInvalidConstraints:
				err = t.emitInvalidConstraints(ctx, originalCatalog, emit)
			case dumpInheritanceRecipes:
				err = t.emitInheritedGenerations(ctx, originalCatalog, emit)
			case dumpInheritanceChecks:
				err = t.emitInheritedChecks(ctx, originalCatalog, emit)
			case dumpInheritedForeignKeys:
				err = t.emitInheritedForeignKeys(originalCatalog, emit)
			case dumpRetiredConstraints:
				err = t.emitRetiredConstraints(originalCatalog, false, emit, setOwner)
				if err == nil {
					err = t.emitRetiredConstraints(originalCatalog, true, emit, setOwner)
				}
			}
			if err != nil {
				return err
			}
			continue
		}
		if declaration, exists := declarations[oid]; exists {
			if domain := domainNodes[oid]; domain != nil && domain.typ.IsDomain() && declaration.owner != "postgres" {
				// Owner metadata does not imply schema CREATE/USAGE. Preserve
				// its captured identity, construct as bootstrap, then transfer only
				// the exact fresh domain through the ordinary owner executor.
				if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(declaration.owner, "'", "''") + "';\n"); err != nil {
					return err
				}
				currentOwner = declaration.owner
				if err := setOwner("postgres"); err != nil {
					return err
				}
				if err := emit(declaration.sql); err != nil {
					return err
				}
				qualified, err := catalogTypeDeclaration(catalog, domain.typ.Usage())
				if err != nil {
					return err
				}
				if err := emit("ALTER DOMAIN " + qualified + " OWNER TO " + pgIdentifier(declaration.owner) + ";\n"); err != nil {
					return err
				}
				continue
			}
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
			original := originalCatalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: oid}].(*relationDescriptor)
			if original.sqlIndex != nil && original.sqlIndex.constraint != nil && original.sqlIndex.constraint.Primary {
				parent := originalCatalog.state.descriptors[original.sqlIndex.parent].(*relationDescriptor)
				for i := range parent.uniqueConstraints {
					if parent.uniqueConstraints[i].Index == original.address {
						if err := emitPhysicalPrimaryCleanup(originalCatalog, parent, &parent.uniqueConstraints[i], emit, setOwner); err != nil {
							return err
						}
						if err := emitPhysicalSelfForeignKeys(originalCatalog, parent, index.owner, true, emit, setOwner); err != nil {
							return err
						}
						if err := emitPhysicalCompetingUniques(originalCatalog, parent, index.owner, true, emit, setOwner); err != nil {
							return err
						}
						break
					}
				}
			}
			if partitionReplayVirtualUniqueRoot(originalCatalog, original) != nil {
				if err := emit(constraintReplay.virtualUniqueRenames(originalCatalog, original)); err != nil {
					return err
				}
			}
			descriptor := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: oid}]
			if constraintReplay.occupied[descriptor.Namespace()] == nil {
				constraintReplay.occupied[descriptor.Namespace()] = make(map[string]bool)
			}
			constraintReplay.occupied[descriptor.Namespace()][descriptor.Name()] = true
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
		t.descriptor = catalog.state.descriptors[t.descriptor.address].(*relationDescriptor)
		inheritedOwnerReplay := t.descriptor.inheritance != nil && t.owner != "postgres"
		if inheritedOwnerReplay {
			// Retain a real captured-role directive before the privileged CREATE;
			// the isolated importer pairs this exact new relation and role.
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(t.owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = t.owner
			if err := setOwner("postgres"); err != nil {
				return err
			}
		} else if err := setOwner(t.owner); err != nil {
			return err
		}
		qualified := quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name)
		// CREATE TABLE OF cannot declare identity/generation or preserve a
		// post-creation array rank. Reconstruct ordinary columns, then attach.
		attachAfterCreate := t.descriptor.ofType != (ObjectAddress{}) && (len(t.identities) != 0 || len(t.descriptor.generations) != 0 || slices.ContainsFunc(t.columns, func(column ColumnDescriptor) bool { return column.DeclaredDimensions != 0 }))
		var line strings.Builder
		if t.descriptor.membership == nil || partitionDumpNeedsAttach(catalog, t.descriptor) {
			typed := t.descriptor.ofType != (ObjectAddress{}) && !attachAfterCreate
			hasElements := !typed || len(t.columns) != 0 || len(t.descriptor.checkConstraints) != 0 || len(t.descriptor.uniqueConstraints) != 0 || len(t.descriptor.exclusionConstraints) != 0 || len(t.descriptor.foreignKeys) != 0
			line.WriteString("CREATE TABLE " + qualified)
			if typed {
				source := catalog.state.descriptors[t.descriptor.ofType].(*typeDescriptor)
				namespace := catalog.state.descriptors[source.namespace].Name()
				line.WriteString(" OF " + quoteIdentifier(namespace) + "." + quoteIdentifier(source.Name()))
			}
			if hasElements {
				line.WriteString(" (")
			}
			localElements := 0
			for i, c := range t.columns {
				provenance := inheritedColumn{local: true, explicitDefault: true, explicitGeneration: true}
				if t.descriptor.inheritance != nil {
					provenance = t.descriptor.inheritance.columns[t.attributes[i]]
				}
				if effective, exists := t.recipes[t.attributes[i]]; exists {
					provenance = effective
				}
				if !provenance.local {
					continue
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				line.WriteString(quoteIdentifier(c.Name))
				if typed {
					line.WriteString(" WITH OPTIONS")
				} else {
					declaration, err := catalogColumnDeclaration(catalog, c)
					if err != nil {
						return err
					}
					line.WriteString(" " + declaration)
					if c.Usage.Collation.ID != c.Usage.Type.Usage().Collation.ID {
						name := collationSQLName(catalog, c.Usage.Collation.ID)
						if name == "" {
							return fmt.Errorf("stuntgres: unsupported stored collation")
						}
						line.WriteString(" COLLATE " + name)
					}
				}
				if definition, exists := t.defaults[t.attributes[i]]; exists && provenance.explicitDefault && !(deferDefaults && t.deferredDefinitions[t.attributes[i]]) {
					text, err := defaultSQL(ctx, catalog, definition.expression)
					if err != nil {
						return err
					}
					line.WriteString(" DEFAULT " + text)
				}
				if provenance.explicitDefault && t.descriptor.inheritance != nil {
					if _, exists := t.defaults[t.attributes[i]]; !exists {
						line.WriteString(" DEFAULT NULL")
					}
				}
				if definition, exists := t.descriptor.generations[t.attributes[i]]; exists && provenance.explicitGeneration {
					var text string
					if t.recipePlaceholders {
						// The genuine owning declaration supplies the full type/typmod.
						// An explicit cast would require unrelated public cast syntax.
						text = "NULL"
					} else {
						var err error
						text, err = (scalarRenderer{owner: t.descriptor}).sql(ctx, catalog, definition.expression)
						if err != nil {
							return err
						}
					}
					line.WriteString(" GENERATED ALWAYS AS (" + text + ") " + definition.kind.sql())
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
				if !c.Nullable && dumpNotNullDeclaration(originalCatalog, t.descriptor, t.attributes[i]) {
					for _, constraint := range t.notNullConstraints {
						if constraint.Attribute == t.attributes[i] && !constraint.NotValid {
							line.WriteString(" CONSTRAINT " + quoteIdentifier(constraint.Name) + " NOT NULL")
							break
						}
					}
				}
			}
			if graph := t.descriptor.inheritance; graph != nil {
				for _, constraint := range t.notNullConstraints {
					if constraint.NotValid {
						continue
					}
					if graph.columns[constraint.Attribute].local || !dumpNotNullDeclaration(originalCatalog, t.descriptor, constraint.Attribute) {
						continue
					}
					if localElements > 0 {
						line.WriteString(", ")
					}
					localElements++
					slot := t.descriptor.indexForAttribute(constraint.Attribute)
					line.WriteString("CONSTRAINT " + quoteIdentifier(constraint.Name) + " NOT NULL " + quoteIdentifier(t.columns[slot].Name))
				}
			}
			for _, c := range orderedChecks(t.descriptor) {
				if c.NotValid || t.deferredChecks[c.Name] {
					continue
				}
				if !dumpCheckDeclaration(originalCatalog, t.descriptor, c) {
					continue
				}
				text, err := (scalarRenderer{owner: t.descriptor, checkReplayPrecision: true}).sql(ctx, catalog, c.expression)
				if err != nil {
					return err
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				line.WriteString("CONSTRAINT " + quoteIdentifier(c.Name) + " CHECK (" + text + ")")
				if c.NoInherit {
					line.WriteString(" NO INHERIT")
				}
			}
			for _, c := range t.descriptor.uniqueConstraints {
				if physicalPrimaryDeferred(t.descriptor, &c) {
					continue
				}
				index, ok := catalog.state.descriptors[c.Index].(*relationDescriptor)
				if !ok {
					return fmt.Errorf("stuntgres: missing unique dump index")
				}
				if _, moved, err := physicalCompetingIndexSource(originalCatalog, index); err != nil {
					return err
				} else if moved {
					continue
				}
				if partitionReplaySeparateConstraint(catalog, index) {
					continue
				}
				text, err := (pgRenderer{view: catalog}).uniqueConstraintSQL(t.descriptor, &c)
				if err != nil {
					return err
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				line.WriteString("CONSTRAINT " + quoteIdentifier(c.Name) + " " + text)
			}
			for _, c := range t.descriptor.exclusionConstraints {
				if c.Parent != 0 {
					continue
				}
				index, ok := catalog.state.descriptors[c.Index].(*relationDescriptor)
				if !ok {
					return fmt.Errorf("stuntgres: missing exclusion dump index")
				}
				if partitionReplaySeparateConstraint(catalog, index) {
					continue
				}
				text, err := indexDefinitionSQL(ctx, catalog, index, indexPresentation{dump: true, exclusion: true})
				if err != nil {
					return err
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				line.WriteString("CONSTRAINT " + quoteIdentifier(c.Name) + " " + text)
			}
			for _, f := range t.descriptor.foreignKeys {
				if f.NotValid || physicalSelfForeignKeyDeferred(t.descriptor, f) || inheritedForeignKeyDeferred(originalCatalog, t.descriptor, &f) {
					continue
				}
				text, err := (pgRenderer{view: catalog}).foreignKeyConstraintSQL(t.descriptor, f)
				if err != nil {
					return err
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				line.WriteString("CONSTRAINT " + quoteIdentifier(f.Name) + " " + text)
			}
			if hasElements {
				line.WriteString(")")
			}
			if graph := t.descriptor.inheritance; graph != nil {
				line.WriteString(" INHERITS (")
				for i, address := range graph.parents {
					if i > 0 {
						line.WriteString(", ")
					}
					parent := catalog.state.descriptors[address].(*relationDescriptor)
					namespace := catalog.state.descriptors[parent.namespace].Name()
					line.WriteString(quoteIdentifier(namespace) + "." + quoteIdentifier(parent.name))
				}
				line.WriteString(")")
			}
		} else {
			var deferred map[int32]bool
			if deferDefaults {
				deferred = t.deferredDefinitions
			}
			definition, err := partitionChildSQL(ctx, catalog, t.descriptor, qualified, deferred)
			if err != nil {
				return err
			}
			line.WriteString(definition)
		}
		key, err := partitionKeySQL(ctx, catalog, t.descriptor)
		if err != nil {
			return err
		}
		line.WriteString(key + ";\n")
		if err := emit(line.String()); err != nil {
			return err
		}
		if inheritedOwnerReplay {
			if err := emit("ALTER TABLE " + qualified + " OWNER TO " + quoteIdentifier(t.owner) + ";\n"); err != nil {
				return err
			}
			if err := setOwner(t.owner); err != nil {
				return err
			}
		}
		if err := t.emitInheritedDefaults(ctx, originalCatalog, deferDefaults, emit); err != nil {
			return err
		}
		if err := t.emitNoInheritNotNull(emit, setOwner); err != nil {
			return err
		}
		if err := t.emitPhysicalNotNull(originalCatalog, emit, setOwner); err != nil {
			return err
		}
		if err := emitPhysicalSelfForeignKeys(originalCatalog, t.descriptor, t.owner, false, emit, setOwner); err != nil {
			return err
		}
		if err := emitPhysicalCompetingUniques(originalCatalog, t.descriptor, t.owner, false, emit, setOwner); err != nil {
			return err
		}
		if attachAfterCreate {
			source := catalog.state.descriptors[t.descriptor.ofType].(*typeDescriptor)
			namespace := catalog.state.descriptors[source.namespace].Name()
			if err := emit("ALTER TABLE " + qualified + " OF " + quoteIdentifier(namespace) + "." + quoteIdentifier(source.Name()) + ";\n"); err != nil {
				return err
			}
		}
		renames, err := constraintReplay.tableRenames(catalog, t.descriptor)
		if err != nil {
			return err
		}
		if err := emit(renames); err != nil {
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
		if err := materialized[i].emitMaterializedSnapshot(ctx, originalCatalog, emit); err != nil {
			return err
		}
	}
	for _, group := range policyGroups {
		if group.owner != currentOwner {
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(group.owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = group.owner
		}
		if err := emit(group.sql); err != nil {
			return err
		}
	}
	for _, group := range securityGroups {
		if group.owner != currentOwner {
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(group.owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = group.owner
		}
		if err := emit(group.sql); err != nil {
			return err
		}
	}
	// User rules are post-data: importing ordinary INSERT rows must not rewrite
	// fixture data before every relation and original stored row exists.
	for _, group := range ruleGroups {
		if group.owner != currentOwner {
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(group.owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = group.owner
		}
		if err := emit(group.sql); err != nil {
			return err
		}
	}
	// INHERITS reconstructs every inherited/merged declaration with rank zero.
	// Repair only the lost metadata after all owner SQL, as genuine PostgreSQL
	// catalog SQL under the authenticated administrator, not as replay comments.
	rankAuthority := false
	for _, event := range eventOrder {
		index, exists := tableIndices[event.oid]
		if event.phase != dumpDeclaration || !exists {
			continue
		}
		table := tables[index]
		graph := table.descriptor.inheritance
		if graph == nil {
			continue
		}
		for i, column := range table.columns {
			if column.DeclaredDimensions == 0 || len(graph.columns[table.attributes[i]].origins) == 0 {
				continue
			}
			if !rankAuthority {
				if err := emit("SET SESSION AUTHORIZATION DEFAULT;\n"); err != nil {
					return err
				}
				rankAuthority = true
			}
			qualified := quoteIdentifier(catalog.state.descriptors[table.descriptor.namespace].Name()) + "." + quoteIdentifier(table.name)
			if err := emit(fmt.Sprintf("UPDATE pg_catalog.pg_attribute SET attndims=%d WHERE attrelid=%s::regclass AND attname=%s;\n", column.DeclaredDimensions, commentLiteral(qualified), commentLiteral(column.Name))); err != nil {
				return err
			}
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

// Restore replaces the persistent logical catalog atomically from Dump SQL.
// Partitioned key/index references are reminted against retained live TEMP
// objects; failed or canceled replacement leaves the old tree usable.
// Native replacement is not a PostgreSQL control API.
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
	var extensionOwners []extensionOwnerRecord
	statements, err := parser.ParseWithOptions(string(data), parser.Options{Context: ctx, StandardConformingStrings: true, LineComment: func(comment string) error {
		ownerRecord, ownerRecognized, ownerErr := decodeExtensionOwners(comment)
		if ownerErr != nil {
			return ownerErr
		}
		if ownerRecognized {
			extensionOwners = append(extensionOwners, ownerRecord)
			return nil
		}
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
	session, err := stage.StartSession(ctx, SessionStart{ID: 1, Authenticated: stage.authorizationDomain.Principal(10)})
	if err != nil {
		return err
	}
	defer func() { session.RequestEnd(CleanClose); _ = session.WaitEnd(context.Background()) }()
	op, err := session.Acquire(ctx)
	if err != nil {
		return err
	}
	defer op.Done()
	defer func() {
		op.restoreDefaultReplay = false
		op.restorePartitionAttach = false
		op.restorePartitionCheck = nil
		op.restorePartitionUsingIndex = nil
		op.restorePartitionUnique = nil
	}()
	var pendingForeignKeys []restoredForeignKeys
	partitionTeardowns := make(map[ObjectAddress]partitionReplayTeardown)
	notNullOrigins := make(map[OID]restoredNotNull)
	var pairedNotNull restoredNotNull
	var pairedPhysical *restoredPhysicalNotNull
	var deferredPhysical *restoredPhysicalNotNull
	physicalIndexes := make(map[ObjectAddress]*relationDescriptor)
	var physicalForeignProvider *restoredPhysicalForeignKeyProvider
	placeholderOrigins := make(map[restoredGenerationKey]restoredGenerationRecipe)
	var generationRepairs []restoredGenerationRecipe
	var lastAuthorization, createOwner authorization.PrincipalRef
	var pairedOwner restoredInheritedOwner
	var pairedDomainOwner restoredDomainOwner
	historicalRefs := make(map[ObjectAddress]ObjectRef)
	historicalAuthority := &restoredHistoryAuthority{facts: make(map[OID]restoredHistoricalFact), archives: make(map[uint64]uint64), sources: make(map[retiredSourceIdentity]retiredSourceDescription), nextArchive: e.retiredArchiveHighWater()}
	for statementIndex, stmt := range statements {
		if pairedPhysical != nil && !pairedPhysical.admits(stmt) {
			return sqlError("0A000", restorePhysicalNotNullBoundary)
		}
		var physicalConstructor *physicalNotNullConstructor
		if alteration, ok := stmt.(*parser.AlterTable); ok {
			physicalConstructor, err = stage.preparePhysicalNotNullConstructor(op, alteration, physicalNotNullFollowing(statements, statementIndex), historicalRefs)
			if err != nil {
				return err
			}
			if physicalConstructor == nil {
				physicalConstructor, err = stage.prepareDeferredPhysicalPrimary(op, alteration, deferredPhysical, historicalRefs)
				if err != nil {
					return err
				}
			}
		}
		if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default && deferredPhysical != nil && !deferredPhysical.ownerDirective {
			deferredPhysical.ownerDirective = true
		} else {
			deferredPhysical = nil
		}
		var physicalIndexBase CatalogRead
		if _, ok := stmt.(*parser.CreateIndex); ok {
			physicalIndexBase = stage.CatalogRead()
		}
		priorAuthorization, ownerForCreate, ownerPair := lastAuthorization, createOwner, pairedOwner
		domainOwnerPair := pairedDomainOwner
		pairedDomainOwner = restoredDomainOwner{}
		lastAuthorization, createOwner = authorization.PrincipalRef{}, authorization.PrincipalRef{}
		pairedOwner = restoredInheritedOwner{}
		if alteration, ok := stmt.(*parser.AlterTable); ok && physicalConstructor == nil {
			declaration, admitted, captureErr := stage.captureInheritedForeignKey(op, alteration, historicalRefs)
			if captureErr != nil {
				return captureErr
			}
			if admitted {
				pendingForeignKeys = append(pendingForeignKeys, declaration)
				physicalForeignProvider = nil
				pairedNotNull = restoredNotNull{}
				continue
			}
		}
		if physicalForeignProvider != nil {
			if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default && !physicalForeignProvider.ownerDirective {
				physicalForeignProvider.ownerDirective = true
			} else if alteration, ok := stmt.(*parser.AlterTable); ok {
				declaration, admitted, captureErr := stage.capturePhysicalSelfForeignKey(op, alteration, physicalForeignProvider, historicalRefs)
				if captureErr != nil {
					return captureErr
				}
				if admitted {
					pendingForeignKeys = append(pendingForeignKeys, declaration)
					pairedNotNull = restoredNotNull{}
					continue
				}
				physicalConstructor, err = stage.preparePhysicalCompetingUnique(op, alteration, physicalForeignProvider, historicalRefs)
				if err != nil {
					return err
				}
				if physicalConstructor == nil {
					physicalForeignProvider = nil
				}
			} else {
				physicalForeignProvider = nil
			}
		}
		var pending restoredForeignKeys
		switch s := stmt.(type) {
		case *parser.AlterOwner:
			if s.Kind == "domain" {
				if err := stage.restoreDomainOwner(op, s, domainOwnerPair); err != nil {
					return err
				}
			} else if err := stage.restoreInheritedOwner(op, s, ownerPair); err != nil {
				return err
			}
		case *parser.CreateTable:
			if declarations := foreignKeyDeclarations(s); len(declarations) != 0 {
				pending, err = stage.captureRestoredForeignKeys(op, declarations)
				if err != nil {
					return err
				}
				stripRestoreForeignKeys(s)
			}
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
				if element.Exclusion != nil {
					for _, key := range element.Exclusion.Index.Keys {
						if err := restoreScalar(key.Expression, true); err != nil {
							return err
						}
					}
					if err := restoreScalar(element.Exclusion.Index.Predicate, true); err != nil {
						return err
					}
				}
			}
		case *parser.AlterDomain:
			if err := restoreDomainAlter(s); err != nil {
				return err
			}
		case *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.CreateEnum, *parser.CreateDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.CreateConversion, *parser.AlterConversion:
		case *parser.CreateExtension:
		case *parser.AlterView:
			if !s.Materialized || (s.Action != "depends" && s.Action != "no_depends") || s.IfExists {
				return sqlError("0A000", "restore accepts only materialized extension dependency ALTER")
			}
		case *parser.CreateView:
			if s.Persistence == "temporary" || len(s.Name.Parts) == 2 && (s.Name.Parts[0].Text == "pg_temp" || strings.HasPrefix(s.Name.Parts[0].Text, "pg_temp_")) {
				return sqlError("0A000", "restore cannot stage non-retractable effects")
			}
		case *parser.CreateQueryTable:
			if s.Form != parser.CreateMaterializedView || !s.NoData {
				return sqlError("0A000", "restore accepts only unpopulated materialized view definitions")
			}
		case *parser.CreateIndex:
		case *parser.AlterIndex:
			if s.Action != "rename" && s.Action != "attach" {
				return sqlError("0A000", "restore accepts only index rename and partition attachment")
			}
		case *parser.CreatePolicy:
		case *parser.CreateRule:
		case *parser.AlterTable:
			if physicalConstructor != nil {
				break
			}
			if rowSecurityActions(s.Actions) {
				break
			}
			if len(s.Actions) == 1 && s.Actions[0].Action == parser.AddCheck && s.Actions[0].Check != nil && !s.Actions[0].Check.NotValid {
				if err := stage.restoreInheritedCheck(op, s, generationRepairs); err != nil {
					return err
				}
				break
			}
			if err := restoreAlterConstraints(s); err != nil {
				return err
			}
		case *parser.AlterTableOf:
			if s.Type == nil {
				return sqlError("0A000", "restore accepts only typed-table attachments")
			}
		case *parser.AlterTableDetachPartition:
			if s.IfExists || s.Only || len(s.Name.Parts) != 2 || len(s.Partition.Parts) != 2 {
				return sqlError("0A000", "restore accepts only unconditional qualified partition detachments")
			}
			record, captureErr := stage.capturePartitionReplayTeardown(op, s)
			if captureErr != nil {
				return captureErr
			}
			if record.child.owner != nil {
				partitionTeardowns[record.child.Address] = record
			}
		case *parser.DropTable:
			if s.IfExists || s.Mode != parser.DropRestrict || len(s.Names) != 1 || len(s.Names[0].Parts) != 2 || !strings.HasPrefix(s.Names[0].Parts[1].Text, partitionReplayStem) {
				return sqlError("0A000", "restore accepts only generated empty partition replay teardown")
			}
			if err := stage.validatePartitionReplayTeardown(op, s.Names[0], partitionTeardowns); err != nil {
				return err
			}
		case *parser.AlterTableAttachPartition:
			if s.IfExists {
				return sqlError("0A000", "restore accepts only unconditional partition attachments")
			}
			for _, tuple := range [2][]parser.PartitionDatum{s.Bound.Lower, s.Bound.Upper} {
				for _, datum := range tuple {
					if datum.Kind == 0 {
						if err := restoreScalar(datum.Expression, true); err != nil {
							return err
						}
					}
				}
			}
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
			if !(s.Action == parser.SetDefault && s.Expression != nil) && !(s.Action == parser.DropDefault && s.Only && len(s.Name.Parts) == 2) {
				return sqlError("0A000", "restore accepts only deferred SET DEFAULT or inherited ONLY DEFAULT alterations")
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
			if s.Default {
				generationRepairs = nil
			}
			if !s.OwnerDirective && !s.Default {
				return sqlError("0A000", "restore accepts only named SET SESSION AUTHORIZATION owner directives or DEFAULT")
			}
		case *parser.Update:
			var repairErr error
			if restoreCatalogName(s.Name, "pg_constraint") && len(s.Assignments) == 2 {
				repairErr = stage.restoreRetiredConstraint(op, s, historicalAuthority)
			} else if restoreCatalogName(s.Name, "pg_constraint") {
				repairErr = stage.restoreNoInheritNotNull(op, s, pairedNotNull)
				if repairErr == nil {
					delete(notNullOrigins, pairedNotNull.constraint)
				}
			} else {
				repairErr = stage.restoreAttributeRank(op, s)
			}
			pairedNotNull = restoredNotNull{}
			if repairErr != nil {
				return repairErr
			}
			continue
		case *parser.Delete:
			if err := stage.restorePhysicalNotNull(op, s, pairedPhysical); err != nil {
				return err
			}
			if pairedPhysical.position == len(pairedPhysical.repairs) {
				physicalForeignProvider, err = stage.capturePhysicalForeignKeyProvider(op, pairedPhysical.repairs[0].relation, historicalRefs)
				if err != nil {
					return err
				}
				if pairedPhysical.primary == nil {
					deferredPhysical = pairedPhysical
					deferredPhysical.ownerDirective = false
				}
				pairedPhysical = nil
			}
			pairedNotNull = restoredNotNull{}
			continue
		case *parser.Insert:
			if len(s.With) != 0 {
				return sqlError("0A000", "restore INSERT WITH clauses are unsupported")
			}
			if s.Returning != nil {
				return sqlError("0A000", "restore INSERT RETURNING is unsupported")
			}
			if s.Conflict != nil {
				return sqlError("0A000", "restore INSERT conflict clauses are unsupported")
			}
			if s.Source != nil {
				return sqlError("0A000", "restore INSERT query sources are unsupported")
			}
			for _, target := range s.Targets {
				for _, step := range target.Steps {
					if step.Subscript == nil {
						continue
					}
					bound := step.Subscript
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
		case *parser.ForeignDDL:
			return sqlError("0A000", "restore cannot replay SQL/MED foreign metadata")
		default:
			return sqlError("0A000", "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION")
		}
		if insertion, ok := stmt.(*parser.Insert); ok && len(generationRepairs) != 0 {
			generationRepairs, err = stage.closeRestoredRecipesForData(op, insertion, generationRepairs)
			if err != nil {
				return err
			}
		}
		if _, ownerDirective := stmt.(*parser.SessionAuthorization); !ownerDirective {
			pairedNotNull = restoredNotNull{}
		}
		if alteration, ok := stmt.(*parser.AlterTable); physicalConstructor == nil && ok && len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.SetNotNull {
			pairedNotNull, err = stage.pairRestoredNotNull(op, alteration, notNullOrigins)
			if err != nil {
				return err
			}
		}
		if alteration, ok := stmt.(*parser.AlterTable); ok && len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.SetExpression {
			if err := stage.pairRestoredGeneration(op, alteration, placeholderOrigins); err != nil {
				return err
			}
		}
		op.restoreTableDestination = false
		if _, ok := stmt.(*parser.CreateTable); ok {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.CreateDomain); ok {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.CreateEnum); ok {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.CreateComposite); ok {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.CreatePolicy); ok {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.CreateRule); ok {
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
		if _, ok := stmt.(*parser.CreateConversion); ok {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.AlterConversion); ok {
			op.restoreTableDestination = true
		}
		if _, ok := stmt.(*parser.CreateExtension); ok {
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
		_, replayDefault := stmt.(*parser.AlterTableDefault)
		op.restoreDefaultReplay = replayDefault
		_, op.restorePartitionAttach = stmt.(*parser.AlterTableAttachPartition)
		if alteration, ok := stmt.(*parser.AlterTable); ok {
			if err := stage.beginPartitionCheckReplay(op, alteration); err != nil {
				return err
			}
			if err := stage.beginPartitionUsingIndexReplay(op, alteration); err != nil {
				return err
			}
			if err := stage.beginPartitionUniqueReplay(op, alteration); err != nil {
				return err
			}
			if len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.AddUnique && op.restorePartitionUnique == nil && physicalConstructor == nil {
				return sqlError("0A000", "restore accepts only captured private virtual unique definitions")
			}
		}
		var result Result
		result, err = stage.Execute(op, stmt, 0, nil)
		op.restoreDefaultReplay = false
		op.restorePartitionAttach = false
		op.restorePartitionCheck = nil
		op.restorePartitionUsingIndex = nil
		op.restorePartitionUnique = nil
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
		if physicalConstructor != nil {
			pairedPhysical, err = stage.capturePhysicalNotNullConstructor(op, physicalConstructor, physicalIndexes)
			if err != nil {
				return err
			}
		}
		if alteration, ok := stmt.(*parser.AlterTable); ok && len(alteration.Actions) == 1 && pairedPhysical == nil && (alteration.Actions[0].Action == parser.AddUnique || alteration.Actions[0].Action == parser.AddUsingIndex) {
			physicalForeignProvider, err = stage.capturePhysicalForeignKeyProvider(op, alteration.Name, historicalRefs)
			if err != nil {
				return err
			}
			if physicalForeignProvider != nil && physicalConstructor != nil && physicalConstructor.competing {
				physicalForeignProvider.permuted = true
			}
		}
		if creation, ok := stmt.(*parser.CreateIndex); ok {
			if index := stage.capturePhysicalImportIndex(physicalIndexBase, creation, historicalRefs); index != nil {
				physicalIndexes[index.address] = index
			}
		}
		if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default {
			auth, authErr := op.Authorization()
			if authErr != nil {
				return authErr
			}
			actor, roleErr := auth.roles.Descriptor(auth.state.CurrentUser)
			if roleErr != nil {
				return roleErr
			}
			if actor.Superuser && priorAuthorization != (authorization.PrincipalRef{}) && priorAuthorization != actor.Ref {
				createOwner = priorAuthorization
			}
			lastAuthorization = actor.Ref
		}
		if _, domain := stmt.(*parser.CreateDomain); domain && ownerForCreate != (authorization.PrincipalRef{}) && result.created != (ObjectRef{}) {
			pairedDomainOwner, err = stage.captureRestoredDomainOwner(op, result.created, ownerForCreate)
			if err != nil {
				return err
			}
		}
		if creation, ok := stmt.(*parser.CreateTable); ok && result.created != (ObjectRef{}) {
			historicalRefs[result.created.Address] = result.created
			captureHistoricalRelation(stage.CatalogRead(), result.created, historicalAuthority.facts)
			stage.captureRestoredNotNulls(result.created, creation, notNullOrigins)
			stage.captureRestoredPlaceholders(result.created, creation, placeholderOrigins)
			if ownerForCreate != (authorization.PrincipalRef{}) && len(creation.Parents) != 0 {
				pairedOwner, err = stage.captureInheritedOwner(op, result.created, ownerForCreate)
				if err != nil {
					return err
				}
			}
		}
		if alteration, altered := stmt.(*parser.AlterTable); altered && slices.ContainsFunc(alteration.Actions, func(a parser.AlterTableAction) bool {
			return a.Action == parser.AddCheck || a.Action == parser.AddNotNull || a.Action == parser.SetNotNull
		}) {
			stage.captureHistoricalFacts(historicalRefs, historicalAuthority.facts)
		}
		if result.created != (ObjectRef{}) && len(pending.definitions) != 0 {
			pending.child = result.created
			if err := stage.resolveRestoredForeignKeyTargets(&pending); err != nil {
				return err
			}
			pendingForeignKeys = append(pendingForeignKeys, pending)
		}
		if alteration, ok := stmt.(*parser.AlterTableDefault); ok {
			if err := stage.validateRestoredDefault(op, alteration); err != nil {
				return err
			}
		}
		if alteration, ok := stmt.(*parser.AlterTable); ok && len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.SetExpression {
			record, captureErr := stage.captureRestoredGeneration(op, alteration)
			if captureErr != nil {
				return captureErr
			}
			generationRepairs = append(generationRepairs, record)
			delete(placeholderOrigins, restoredGenerationKey{record.ref.Address, record.attribute})
		}
	}
	if pairedPhysical != nil {
		return sqlError("0A000", restorePhysicalNotNullBoundary)
	}
	if err := stage.bindRestoredForeignKeys(ctx, op, pendingForeignKeys); err != nil {
		return err
	}
	if err := stage.importExtensionOwners(ctx, op, extensionOwners); err != nil {
		return err
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
	case parser.SQLJSONExpr:
		for _, child := range r.Args {
			if err := restoreScalar(child, stored); err != nil {
				return err
			}
		}
		if r.Query != nil {
			return restoreSQLJSONQuery(r.Query, stored)
		}
		return nil
	case parser.JSONPredicateExpr:
		return restoreScalar(r.Child, stored)
	case parser.SubqueryExpr:
		if r.Left != nil {
			if err := restoreScalar(r.Left, stored); err != nil {
				return err
			}
		}
		if err := restoreSQLJSONQuery(r.Query, stored); err != nil {
			return err
		}
		if !stored {
			return fmt.Errorf("stuntgres: invalid restore scalar")
		}
		return nil
	case parser.FunctionExpr:
		if !stored {
			if len(r.Name.Parts) < 1 || len(r.Name.Parts) > 2 {
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
			switch r.Name.Parts[len(r.Name.Parts)-1].Text {
			case "split_part", "string_to_array", "parse_ident":
			case "acldefault", "makeaclitem":
				// These typed scalar constructors are also emitted by the
				// ordinary lossless datum renderer. Their binder owns the
				// signature and value checks; recurse below to reject parameters.
			case "array_position", "array_positions", "array_remove", "array_replace":
			case "array_dims", "array_length", "array_lower", "array_upper", "array_ndims", "cardinality":
			case "array_append", "array_prepend", "array_cat", "array_fill", "array_reverse", "array_sort", "trim_array", "array_to_string":
			case "isempty", "lower_inc", "upper_inc", "lower_inf", "upper_inf", "range_merge":
			case "json_object", "jsonb_object", "jsonb_set", "jsonb_set_lax", "jsonb_insert", "json_strip_nulls", "jsonb_path_query_array", "jsonb_path_exists", "jsonb_path_match", "jsonb_path_query_first":
			case "json_typeof", "jsonb_typeof", "jsonb_pretty", "json_array_length", "jsonb_array_length":
			case "to_json", "to_jsonb", "array_to_json", "row_to_json", "json_build_array", "jsonb_build_array", "json_build_object", "jsonb_build_object":
			case "btrim", "ltrim", "rtrim", "normalize", "is_normalized", "lower", "upper", "casefold", "initcap", "bit_length", "char_length", "character_length", "length", "octet_length", "position", "strpos", "overlay", "replace", "translate", "starts_with", "lpad", "rpad", "repeat", "reverse", "ascii", "chr", "to_ascii", "unistr", "unicode_assigned":
			case "to_bin", "to_oct", "to_hex":
			case "pg_size_bytes", "pg_size_pretty":
			case "age", "isfinite":
			case "overlaps":
			case "date_trunc", "date_bin":
			case "timezone", "date_add", "date_subtract":
			case "justify_days", "justify_hours", "justify_interval":
			case "make_date", "make_time", "make_timestamp", "make_timestamptz", "make_interval":
			case "gen_random_uuid", "uuidv4", "uuidv7":
			case "uuid_extract_timestamp", "uuid_extract_version":
			case "format", "concat", "concat_ws", "quote_ident", "quote_literal", "quote_nullable", "pg_client_encoding":
			case "convert", "convert_from", "convert_to", "encode", "decode", "crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512", "bit_count", "get_bit", "get_byte", "set_bit", "set_byte":
			case "regexp_count", "regexp_instr", "regexp_substr", "regexp_like", "regexp_match", "regexp_replace", "regexp_split_to_array":
				_, _, _, valid := functions.RegexpSignature(r.Name.Parts[len(r.Name.Parts)-1].Text, len(r.Args), true)
				if !valid || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			case "like_escape":
				if len(r.Name.Parts) != 2 || r.Name.Parts[0].Text != "pg_catalog" || len(r.Args) != 2 || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			case "similar_to_escape":
				if r.Syntax != parser.SimilarEscapeFunctionSyntax || len(r.Name.Parts) != 2 || r.Name.Parts[0].Text != "pg_catalog" || len(r.Args) < 1 || len(r.Args) > 2 || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			case "left", "right":
				if len(r.Args) != 2 || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			case "substring", "substr":
				if (len(r.Args) != 2 && len(r.Args) != 3) || r.Variadic {
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
	case parser.FieldExpr:
		return restoreScalar(r.Base, stored)
	case parser.RowExpr:
		for _, field := range r.Fields {
			if err := restoreScalar(field, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.InListExpr:
		if err := restoreScalar(r.Left, stored); err != nil {
			return err
		}
		for _, item := range r.Items {
			if err := restoreScalar(item, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.ArrayComparisonExpr:
		if err := restoreScalar(r.Left, stored); err != nil {
			return err
		}
		return restoreScalar(r.Right, stored)
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
			if r.SQLSimilar {
				if (spelling != "~" && spelling != "!~") || len(r.Args) != 2 {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
				normalizer, ok := r.Args[1].(parser.FunctionExpr)
				if !ok || normalizer.Syntax != parser.SimilarEscapeFunctionSyntax {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			} else if functions.PosixName(spelling) && len(r.Args) != 2 && (spelling != "~" || len(r.Args) != 1) {
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
