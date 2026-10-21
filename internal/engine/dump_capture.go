package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
)

// dumpSchema is one ordinary namespace Dump creates.
type dumpSchema struct {
	name, owner string
	configured  bool
}

// dumpView is one owned definition statement: a view, extension or index.
type dumpView struct {
	owner, sql string
}

// dumpComment is a schema comment that differs from its baseline.
type dumpComment struct{ name, text string }

// dumpCapture is everything Dump reads from the engine. captureDumpLocked
// fills it under Engine.mu; after the unlock Dump reads only the capture.
type dumpCapture struct {
	// catalogView binds private key names until the terminal renames;
	// originalCatalog keeps the captured names for later definitions and
	// values.
	catalogView, originalCatalog catalog.Read
	originalRoles, domainRoles   authorization.Read
	constraintReplay             *partitionConstraintReplay
	recipePlan                   map[ids.ObjectAddress]*dumpRelation
	aclOutput                    capturedACLItemOutput

	preamble       []string
	schemas        []dumpSchema
	schemaComments []dumpComment
	sequences      []sequence.Dump
	domains        []*catalog.Type
	collations     []*catalog.Collation
	conversions    []*catalog.Conversion
	operators      []*catalog.Operator
	sqlRoutines    []*catalog.Procedure
	tables         []dumpRelation
	materialized   []dumpRelation
	views          []dumpView
	indexes        []dumpView
	viewIndices    map[ids.OID]int
	tableIndices   map[ids.OID]int
	indexIndices   map[ids.OID]int
	composites     map[ids.OID]*catalog.Relation
	graph          dump.Graph
	// Deferral removes only eligible definitions' edges. Inline consumers keep
	// their prerequisites, including providers shared with deferred defaults.
	inlinePredecessors map[ids.OID][]ids.OID
	deferDefaults      bool
}

// captureDumpLocked reads the catalog, roles, storage and sequence state Dump
// needs. The caller holds Engine.mu and releases it once on return.
func (e *Engine) captureDumpLocked(ctx context.Context) (*dumpCapture, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, descriptor := range e.catalog.state.Descriptors() {
		_, foreign := descriptor.(*catalog.ForeignObject)
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Foreign != nil {
			foreign = true
		}
		if foreign {
			return nil, fmt.Errorf("stuntgres: dump cannot serialize SQL/MED foreign metadata")
		}
	}
	if e.catalog.state.DefaultACLCount() != 0 {
		return nil, fmt.Errorf("stuntgres: dump cannot serialize default privileges")
	}
	var invalid *catalog.Relation
	var invalidSchema string
	for _, descriptor := range e.catalog.state.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		namespace := e.catalog.state.MustNamespace(index.Namespace())
		if namespace.Temporary != (ids.SessionToken{}) || index.SQLIndex.Ready && index.SQLIndex.Valid && index.SQLIndex.Live {
			continue
		}
		parent, ok := e.catalog.state.Relation(index.SQLIndex.Parent)
		if ok && parent.Partition != nil && index.SQLIndex.Ready && index.SQLIndex.Live {
			continue
		}
		if invalid == nil || namespace.Name() < invalidSchema || namespace.Name() == invalidSchema && index.Name() < invalid.Name() {
			invalid, invalidSchema = index, namespace.Name()
		}
	}
	if invalid != nil {
		return nil, fmt.Errorf("stuntgres: dump cannot serialize invalid index %s.%s", catalog.QuoteIdentifier(invalidSchema), catalog.QuoteIdentifier(invalid.Name()))
	}
	tables := make([]dumpRelation, 0)
	catalogView := e.catalog.read(e.catalog.state)
	originalCatalog := catalogView
	originalRoles := e.roles.read(e.catalog.owner(), originalCatalog, e.roles.published)
	recipePlan, err := capturedGenerationReplayPlan(originalCatalog)
	if err != nil {
		return nil, err
	}
	aclOutput := capturedACLItemOutput{owner: e.catalog.owner(), roles: originalRoles, lossless: true}
	ctx = aclItemOutputContext(ctx, aclOutput)
	if err := preflightACLItemDump(ctx, originalCatalog, e.storage.Relations, e.visibility.CommitSeq(), aclOutput); err != nil {
		return nil, err
	}
	// Declarations bind private key names until the terminal renames. Later
	// definitions and values must render against the original captured names.
	catalogView, constraintReplay := partitionConstraintDumpCatalog(catalogView)
	domainRoles := e.roles.read(e.catalog.owner(), catalogView, e.roles.published)
	if err := e.validateTemporaryCollationDump(ctx, catalogView); err != nil {
		return nil, err
	}
	var domains []*catalog.Type
	var collations []*catalog.Collation
	var conversions []*catalog.Conversion
	var operators []*catalog.Operator
	var sqlRoutines []*catalog.Procedure
	var schemas []dumpSchema
	var sequences []sequence.Dump
	var views []dumpView
	var materialized []dumpRelation
	var indexes []dumpView
	graph := make(dump.Graph)
	viewIndices := make(map[ids.OID]int)
	tableIndices := make(map[ids.OID]int)
	indexIndices := make(map[ids.OID]int)
	composites := make(map[ids.OID]*catalog.Relation)
	// Deferral removes only eligible definitions' edges. Inline consumers keep
	// their prerequisites, including providers shared with deferred defaults.
	var inlinePredecessors map[ids.OID][]ids.OID
	deferDefaults := false
	var schemaComments []dumpComment
	// Reconstruct bootstrap identity changes before creating ordinary namespaces.
	// These statements run only in Restore's isolated catalog under bootstrap
	// authority; ACLs are reproduced by normal owner-transfer semantics.
	var preamble []string
	for address, name := range e.foreignNamespaces {
		ns, ok := e.catalog.state.Namespace(address)
		if !ok || ns.Name() != name {
			return nil, fmt.Errorf("stuntgres: dump cannot serialize a renamed or retired configured foreign namespace")
		}
	}
	type builtinSchema struct {
		original, name, owner, baselineOwner string
		moved                                bool
	}
	var builtins []builtinSchema
	for _, b := range []struct {
		oid  ids.OID
		name string
	}{{ids.PublicNamespaceOID, "public"}, {ids.PGCatalogNamespaceOID, "pg_catalog"}, {ids.PGToastNamespaceOID, "pg_toast"}, {13229, "information_schema"}} {
		d, exists := e.catalog.state.Find(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: b.oid})
		if !exists {
			preamble = append(preamble, "DROP SCHEMA "+catalog.QuoteIdentifier(b.name)+";\n")
			continue
		}
		ns := d.(*catalog.Namespace)
		role, err := domainRoles.Descriptor(ns.Owner)
		if err != nil {
			return nil, err
		}
		baselineOwner := "postgres"
		if b.oid == ids.PublicNamespaceOID {
			baselineOwner = "pg_database_owner"
		}
		builtins = append(builtins, builtinSchema{original: b.name, name: ns.Name(), owner: role.Name, baselineOwner: baselineOwner})
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
			preamble = append(preamble, "ALTER SCHEMA "+catalog.QuoteIdentifier(b.original)+" RENAME TO "+catalog.QuoteIdentifier(b.name)+";\n")
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
				if _, exists := e.catalog.state.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: candidate}); !exists {
					temporary = candidate
					break
				}
			}
			preamble = append(preamble, "ALTER SCHEMA "+catalog.QuoteIdentifier(b.original)+" RENAME TO "+catalog.QuoteIdentifier(temporary)+";\n")
			b.original = temporary
			break
		}
	}
	for _, b := range builtins {
		if b.owner != b.baselineOwner {
			preamble = append(preamble, "ALTER SCHEMA "+catalog.QuoteIdentifier(b.name)+" OWNER TO "+catalog.QuoteIdentifier(b.owner)+";\n")
		}
	}
	for a, d := range e.catalog.state.Descriptors() {
		if ns, ok := e.catalog.state.Namespace(d.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
			continue
		}
		r, ok := d.(*catalog.Relation)
		if !ok {
			switch t := d.(type) {
			case *catalog.Database:
				continue // Destination attachment authority is never serialized.
			case *catalog.Parameter:
				return nil, fmt.Errorf("stuntgres: dump cannot serialize parameter ACL")
			case *catalog.Tablespace:
				if err := (catalog.TablespaceProvider{}).Validate(catalogView, t); err != nil {
					return nil, err
				}
				if t.Owner != e.roles.domain.Principal(authorization.BootstrapPrincipalID) || t.ACL.Explicit {
					return nil, fmt.Errorf("stuntgres: dump cannot serialize altered tablespace authority")
				}
				continue
			case *catalog.Operator:
				if err := (routine.OperatorProvider{}).Validate(catalogView, t); err != nil {
					return nil, err
				}
				if !routine.OperatorSeeded(t) {
					operators = append(operators, t)
				}
				continue
			case *catalog.Procedure:
				if t.SQL != nil {
					if err := (procedureProvider{}).Validate(catalogView, t); err != nil {
						return nil, err
					}
					sqlRoutines = append(sqlRoutines, t)
					continue
				}
				if t.Foreign != nil {
					if err := (procedureProvider{}).Validate(catalogView, t); err != nil {
						return nil, err
					}
					if t.ACL.Explicit || t.Owner != e.roles.domain.Principal(10) {
						return nil, fmt.Errorf("stuntgres: dump cannot serialize altered native foreign capability authority")
					}
					continue
				}
				if t.Builtin != nil {
					if err := (procedureProvider{}).Validate(catalogView, t); err != nil {
						return nil, err
					}
					if t.Owner != e.roles.domain.Principal(authorization.BootstrapPrincipalID) || t.ACL.Explicit {
						return nil, fmt.Errorf("stuntgres: dump cannot serialize altered builtin routine authority")
					}
					continue
				}
				if t.Conversion.Name() != "" {
					if err := (procedureProvider{}).Validate(catalogView, t); err != nil {
						return nil, err
					}
					if t.Owner != e.roles.domain.Principal(10) || t.ACL.Explicit {
						return nil, fmt.Errorf("stuntgres: dump cannot serialize altered builtin conversion routine authority")
					}
					continue
				}
				if t.ACL.Explicit {
					return nil, fmt.Errorf("stuntgres: dump cannot serialize explicit routine ACL")
				}
				if catalog.ExtensionMembership(t) == (ids.ObjectAddress{}) {
					return nil, fmt.Errorf("stuntgres: dump cannot serialize loose extension member")
				}
				continue
			case *catalog.TSTemplate, *catalog.TSDictionary:
				if catalog.ExtensionMembership(t) == (ids.ObjectAddress{}) {
					return nil, fmt.Errorf("stuntgres: dump cannot serialize loose extension member")
				}
				continue
			case *catalog.Extension:
				if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(catalogView, t); err != nil {
					return nil, err
				}
				ns := catalogView.MustNamespace(t.TargetNamespace)
				if ns.Temporary != (ids.SessionToken{}) {
					return nil, fmt.Errorf("stuntgres: temporary extension installation is not portable")
				}
				principal, err := domainRoles.Descriptor(t.Owner)
				if err != nil {
					return nil, err
				}
				metadata, err := extension.OwnerMetadata(catalogView, domainRoles, t)
				if err != nil {
					return nil, err
				}
				text := "CREATE EXTENSION " + catalog.QuoteIdentifier(t.Name()) + " WITH SCHEMA " + catalog.QuoteIdentifier(ns.Name()) + " VERSION '" + strings.ReplaceAll(t.Version, "'", "''") + "';\n" + metadata
				graph[dump.Event{OID: t.Address().ObjectID}] = nil
				viewIndices[t.Address().ObjectID] = len(views)
				views = append(views, dumpView{principal.Name, text})
				continue
			case *catalog.Type:
				// A recipe can regenerate synthetic type defaults, never a real
				// explicit ACL (even one equal to the effective baseline).
				if t.ACL.Explicit && !typedef.DefaultDomainACL(t) {
					message := "stuntgres: dump cannot serialize changed type ACL"
					if t.SQLType.IsDomain() || t.SQLType.IsEnum() {
						message = "stuntgres: dump cannot serialize changed domain ACL"
					} else if t.SQLType.IsComposite() {
						message = "stuntgres: dump cannot serialize changed composite ACL"
					}
					return nil, fmt.Errorf("%s", message)
				}
				if t.SQLType.IsQueryInt() || t.SQLType.IsCube() || t.Extension != (ids.ObjectAddress{}) {
					if err := (typeProvider{}).Validate(catalogView, t); err != nil {
						return nil, err
					}
					continue
				}
				if t.SQLType.IsUserDefined() && !t.SQLType.IsComposite() {
					domains = append(domains, t)
				} else if t.SQLType.IsArray() {
					if err := (typeProvider{}).Validate(catalogView, t); err != nil {
						return nil, err
					}
				} else if t.Namespace().ObjectID != ids.PGCatalogNamespaceOID && t.Namespace().ObjectID != 13229 && t.OwningRelation == (ids.ObjectAddress{}) {
					return nil, fmt.Errorf("stuntgres: dump cannot serialize unsupported user type")
				}
				continue
			case *catalog.Collation:
				if err := (catalog.CollationProvider{}).Validate(catalogView, t); err != nil {
					return nil, err
				}
				if !t.Seeded() {
					collations = append(collations, t)
				}
				continue
			case *catalog.Conversion:
				if err := (catalog.ConversionProvider{}).Validate(catalogView, t); err != nil {
					return nil, err
				}
				if !t.Seeded() {
					conversions = append(conversions, t)
				}
				continue
			}
			if d.Kind() == ids.NamespaceObject {
				ns, valid := d.(*catalog.Namespace)
				if valid && ns.Temporary != (ids.SessionToken{}) {
					continue
				}
				if valid && ns.Comment != namespaceCommentBaseline(a.ObjectID) {
					schemaComments = append(schemaComments, dumpComment{ns.Name(), ns.Comment})
				}
				if valid && (a.ObjectID == ids.PublicNamespaceOID || a.ObjectID == ids.PGCatalogNamespaceOID || a.ObjectID == ids.PGToastNamespaceOID || a.ObjectID == 13229) &&
					!bootstrapNamespaceDumpable(ns) {
					return nil, fmt.Errorf("stuntgres: dump cannot serialize changed builtin namespace ACL")
				}
				if valid && a.ObjectID != ids.PublicNamespaceOID && a.ObjectID != ids.PGCatalogNamespaceOID && a.ObjectID != ids.PGToastNamespaceOID && a.ObjectID != 13229 {
					if ns.ACL.Explicit {
						return nil, fmt.Errorf("stuntgres: dump cannot serialize explicit namespace ACL")
					}
					role, err := domainRoles.Descriptor(ns.Owner)
					if err != nil {
						return nil, err
					}
					_, configured := e.foreignNamespaces[a]
					schemas = append(schemas, dumpSchema{name: ns.Name(), owner: role.Name, configured: configured})
				}
				continue
			}
			return nil, fmt.Errorf("stuntgres: unsupported catalog descriptor kind")
		}
		if r.RelationKind == catalog.CatalogRelation {
			continue
		}
		ns, ok := e.catalog.state.Namespace(r.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: relation namespace is missing")
		}
		if ns.Temporary != (ids.SessionToken{}) {
			continue
		}
		if r.Security.ACL.Explicit || len(r.Security.ColumnACL) != 0 {
			return nil, fmt.Errorf("stuntgres: dump cannot serialize explicit relation or column ACL")
		}
		principal, err := domainRoles.Descriptor(r.Owner)
		if err != nil {
			return nil, err
		}
		if r.RelationKind == catalog.CompositeRelation {
			composites[r.Address().ObjectID] = r
			graph[dump.Event{OID: r.Address().ObjectID}] = nil
			continue
		}
		if r.SQLIndex != nil {
			// Key declarations and their table constraints must use the same
			// captured private-name projection, including deferred local keys.
			r = catalogView.MustRelation(r.Address())
			virtual := partitionReplayVirtualUniqueRoot(catalogView, r)
			if virtual != nil && virtual.Address() != r.Address() {
				continue
			}
			if r.SQLIndex.Constraint != nil && !partitionReplaySeparateConstraint(catalogView, r) {
				continue
			}
			var sql string
			var err error
			if virtual != nil {
				sql, err = partitionVirtualUniqueDefinitionSQL(catalogView, r)
			} else {
				sql, err = indexDefinitionSQL(ctx, catalogView, r, indexPresentation{dump: true})
			}
			if err != nil {
				return nil, err
			}
			oid := r.Address().ObjectID
			if partitionReplayUsingIndex(catalogView, r) {
				parent := catalogView.MustRelation(r.SQLIndex.Parent)
				kind := "UNIQUE"
				if r.SQLIndex.Constraint.Primary {
					kind = "PRIMARY KEY"
				}
				sql += "ALTER TABLE " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(parent.Name()) + " ADD CONSTRAINT " + catalog.QuoteIdentifier(r.Name()) + " " + kind + " USING INDEX " + catalog.QuoteIdentifier(r.Name()) + ";\n"
			}
			event := dump.Event{OID: oid}
			graph[event] = []dump.Event{{OID: r.SQLIndex.Parent.ObjectID}}
			for _, key := range r.SQLIndex.Keys {
				if key.Derived == nil {
					continue
				}
				for _, dependency := range key.Derived.Dependencies {
					predecessor, err := dumpPredecessor(catalogView, dependency.Address, r.SQLIndex.Parent)
					if err != nil {
						return nil, err
					}
					source := dump.Event{OID: predecessor}
					if predecessor != 0 && !slices.Contains(graph[event], source) {
						graph[event] = append(graph[event], source)
					}
				}
			}
			indexIndices[oid] = len(indexes)
			indexes = append(indexes, dumpView{principal.Name, sql})
			continue
		}
		if r.View != nil {
			// Materialized definitions preserve their analyzed presentation
			// without synthetic source aliases. Their explicit empty visibility
			// path below qualifies user types for portable, executable dump SQL.
			presentation := viewPresentation{columns: r.Columns, deparse: r.RelationKind == catalog.MaterializedViewRelation}
			if presentation.deparse {
				// A nil path is diagnostic naming's unqualified mode. Dump's
				// canonical materialized renderer must qualify user types too.
				presentation.path = []ids.ObjectAddress{}
			}
			body, err := viewOf(r).selectSQL(ctx, catalogView, presentation)
			if err != nil {
				return nil, err
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
			if len(viewOf(r).options) != 0 {
				installed := make([]string, len(viewOf(r).options))
				for i, option := range viewOf(r).options {
					name, value, _ := strings.Cut(option, "=")
					installed[i] = catalog.QuoteIdentifier(name) + "=" + dump.CommentLiteral(value)
				}
				options = " WITH (" + strings.Join(installed, ", ") + ")"
			}
			sql := "CREATE VIEW " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(r.Name()) + columns +
				options + " AS " + body + ";\n"
			if r.RelationKind == catalog.MaterializedViewRelation {
				sql = "CREATE MATERIALIZED VIEW " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(r.Name()) + columns + " AS " + body + " WITH NO DATA;\n"
				stored := e.storage.Relations[a]
				if stored == nil {
					return nil, fmt.Errorf("stuntgres: materialized view storage is missing")
				}
				materialized = append(materialized, dumpRelation{owner: principal.Name, schema: ns.Name(), name: r.Name(), columns: r.Columns, attributes: r.Attributes, versions: stored.Versions, ends: stored.Ends, frontier: e.visibility.CommitSeq(), descriptor: r})
			}
			// Capture every source occurrence and expression dependency from the
			// same immutable catalog used to render the definition. Attribute
			// references depend on their parent relation, not its column OID.
			predecessors := make(map[ids.OID]bool)
			addPredecessor := func(address ids.ObjectAddress) error {
				predecessor, err := dumpPredecessor(e.catalog.read(e.catalog.state), address, r.Address())
				if err != nil {
					return err
				}
				if predecessor != 0 {
					predecessors[predecessor] = true
				}
				return nil
			}
			for _, address := range r.AutoExtensions {
				extension, ok := e.catalog.state.Extension(address)
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing materialized extension dump provider")
				}
				if err := addPredecessor(address); err != nil {
					return nil, err
				}
				sql += "ALTER MATERIALIZED VIEW " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(r.Name()) + " DEPENDS ON EXTENSION " + catalog.QuoteIdentifier(extension.Name()) + ";\n"
			}
			for _, leaf := range viewOf(r).Sources() {
				if err := addPredecessor(leaf.ref.Address); err != nil {
					return nil, err
				}
			}
			for _, dependency := range viewOf(r).Dependencies() {
				if err := addPredecessor(dependency.Ref.Address); err != nil {
					return nil, err
				}
			}
			for _, literal := range viewOf(r).Literals() {
				if err := regclassReplayReferences(e.catalog.read(e.catalog.state), literal, addPredecessor); err != nil {
					return nil, err
				}
			}
			oid := r.Address().ObjectID
			event := dump.Event{OID: oid}
			graph[event] = make([]dump.Event, 0, len(predecessors))
			for predecessor := range predecessors {
				graph[event] = append(graph[event], dump.Event{OID: predecessor})
			}
			viewIndices[oid] = len(views)
			views = append(views, dumpView{principal.Name, sql})
			continue
		}
		if r.RelationKind == catalog.SequenceRelation {
			g := e.storage.Sequences[a]
			if g == nil {
				return nil, fmt.Errorf("stuntgres: sequence state missing")
			}
			entry := sequence.Dump{Owner: principal.Name, Schema: ns.Name(), Name: r.Name(), Options: *r.Sequence, State: *g, Comment: r.Comment}
			if owned := r.Sequence.Owned; owned != (ids.ObjectAddress{}) {
				tableAddress := owned
				tableAddress.SubID = 0
				table, ok := e.catalog.state.Relation(tableAddress)
				if !ok {
					return nil, fmt.Errorf("stuntgres: sequence ownership target missing")
				}
				tableSchema := e.catalog.state.Get(table.Namespace()).Name()
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
			sequences = append(sequences, entry)
			continue
		}
		stored := e.storage.Relations[a]
		if stored == nil && r.Partition == nil {
			return nil, fmt.Errorf("stuntgres: relation storage is missing")
		}
		oid := r.Address().ObjectID
		var predecessors []ids.OID
		if r.Inheritance != nil {
			for _, parent := range r.Inheritance.Parents {
				predecessor, err := dumpPredecessor(catalogView, parent, r.Address())
				if err != nil {
					return nil, err
				}
				predecessors = append(predecessors, predecessor)
			}
		}
		predecessors, err = partitionDumpPredecessors(catalogView, r, predecessors)
		if err != nil {
			return nil, err
		}
		for _, check := range r.Constraints.Check {
			if check.NotValid || recipePlan[r.Address()] != nil && recipePlan[r.Address()].deferredChecks[check.Name] {
				continue
			}
			add := func(address ids.ObjectAddress) error {
				predecessor, err := dumpPredecessor(catalogView, address, r.Address())
				if err != nil {
					return err
				}
				if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
					predecessors = append(predecessors, predecessor)
				}
				return nil
			}
			if err := regclassReplayReferences(catalogView, check.Expression, add); err != nil {
				return nil, err
			}
			for _, dependency := range check.Dependencies {
				if err := add(dependency.Ref.Address); err != nil {
					return nil, err
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
				deferDefaults = deferDefaults || definition.Expression.ResultUsage().Type.ContainsRegclass()
			}
			addPredecessor := func(address ids.ObjectAddress) error {
				owner := r.Address()
				if eligible {
					owner = ids.ObjectAddress{}
				}
				predecessor, err := dumpPredecessor(catalogView, address, owner)
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
			if err := regclassReplayReferences(catalogView, definition.Expression, addPredecessor); err != nil {
				return nil, err
			}
			for _, dependency := range definition.Dependencies {
				if err := addPredecessor(dependency.Address); err != nil {
					return nil, err
				}
			}
		}
		for _, definition := range r.ColumnMeta.Generations {
			if recipePlan[r.Address()] != nil {
				continue
			}
			for _, dependency := range definition.Dependencies {
				predecessor, err := dumpPredecessor(catalogView, dependency.Address, r.Address())
				if err != nil {
					return nil, err
				}
				if predecessor != 0 && !slices.Contains(predecessors, predecessor) {
					predecessors = append(predecessors, predecessor)
				}
			}
		}
		graph[dump.Event{OID: oid}] = dump.DeclarationEvents(predecessors)
		if len(deferredPredecessors) != 0 && !deferDefaults {
			if inlinePredecessors == nil {
				inlinePredecessors = make(map[ids.OID][]ids.OID)
			}
			inlinePredecessors[oid] = predecessors
			combined := slices.Clone(predecessors)
			for _, predecessor := range deferredPredecessors {
				if !slices.Contains(combined, predecessor) {
					combined = append(combined, predecessor)
				}
			}
			graph[dump.Event{OID: oid}] = dump.DeclarationEvents(combined)
		}
		tableIndices[oid] = len(tables)
		linked := make(map[ids.ObjectAddress]sequence.Dump, len(r.ColumnMeta.Identities))
		for _, identity := range r.ColumnMeta.Identities {
			relation := e.catalog.state.MustRelation(identity.Sequence)
			linked[identity.Sequence] = sequence.Dump{Owner: principal.Name, Schema: ns.Name(), Name: relation.Name(), Options: *relation.Sequence, State: *e.storage.Sequences[identity.Sequence], Comment: relation.Comment}
		}
		var versions []storage.RowVersion
		var ends *storage.DeletionRoot
		if stored != nil {
			versions, ends = stored.Versions, stored.Ends
		}
		tables = append(tables, dumpRelation{owner: principal.Name, schema: ns.Name(), name: r.Name(), columns: r.Columns, attributes: r.Attributes, versions: versions, ends: ends, frontier: e.visibility.CommitSeq(), comment: r.Comment, columnComments: r.ColumnMeta.Comments, notNullConstraints: r.Constraints.NotNull, defaults: r.ColumnMeta.Defaults, deferredDefinitions: deferredDefinitions, descriptor: r, identities: r.ColumnMeta.Identities, identitySequences: linked})
		if selected := recipePlan[r.Address()]; selected != nil {
			tables[len(tables)-1].recipePlaceholders = true
			tables[len(tables)-1].recipeComponent = selected.recipeComponent
			tables[len(tables)-1].deferredChecks = selected.deferredChecks
		}
	}
	return &dumpCapture{
		catalogView:        catalogView,
		originalCatalog:    originalCatalog,
		originalRoles:      originalRoles,
		domainRoles:        domainRoles,
		constraintReplay:   constraintReplay,
		recipePlan:         recipePlan,
		aclOutput:          aclOutput,
		preamble:           preamble,
		schemas:            schemas,
		schemaComments:     schemaComments,
		sequences:          sequences,
		domains:            domains,
		collations:         collations,
		conversions:        conversions,
		operators:          operators,
		sqlRoutines:        sqlRoutines,
		tables:             tables,
		materialized:       materialized,
		views:              views,
		indexes:            indexes,
		viewIndices:        viewIndices,
		tableIndices:       tableIndices,
		indexIndices:       indexIndices,
		composites:         composites,
		graph:              graph,
		inlinePredecessors: inlinePredecessors,
		deferDefaults:      deferDefaults,
	}, nil
}
