package engine

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/session"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/typedef"
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
	_, err := e.ReplaceCatalog(ctx, fresh, fresh.CatalogRead())
	return err
}

// Namespace ACL entry order and split entries do not affect reproducibility.
// Principal/grantor references do: losing them would change DROP dependencies.
func bootstrapNamespaceDumpable(d *catalog.Namespace) bool {
	if d.Address().ObjectID == ids.PGToastNamespaceOID {
		return !d.ACL.Explicit && len(d.ACL.Entries) == 0
	}
	if !d.ACL.Explicit {
		return false
	}
	publicRights := authorization.UsagePrivilege
	var publicUse, publicOptions, ownerUse, ownerOptions authorization.Privileges
	for _, entry := range d.ACL.Entries {
		if entry.Grantor != d.Owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (authorization.PrincipalRef{}) {
				return false
			}
			publicUse |= entry.Privileges
			publicOptions |= entry.GrantOptions
		} else {
			if entry.Grantee.Role != d.Owner {
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
	capture, err := e.captureDumpLocked(ctx)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	ctx = aclItemOutputContext(ctx, capture.aclOutput)
	script, err := buildDumpScript(ctx, capture)
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
	if len(script.sqlRoutines) != 0 {
		if err := emit(routine.RestoreDirective + "\n"); err != nil {
			return err
		}
	}
	for _, line := range script.preamble {
		if err := emit(line); err != nil {
			return err
		}
	}
	sort.Slice(script.schemas, func(i, j int) bool { return script.schemas[i].name < script.schemas[j].name })
	for _, s := range script.schemas {
		prefix := "CREATE SCHEMA "
		if s.configured {
			prefix += "IF NOT EXISTS "
		}
		if err := emit(prefix + catalog.QuoteIdentifier(s.name) + " AUTHORIZATION " + catalog.QuoteIdentifier(s.owner) + ";\n"); err != nil {
			return err
		}
		if s.configured && s.owner != "postgres" {
			if err := emit("ALTER SCHEMA " + catalog.QuoteIdentifier(s.name) + " OWNER TO " + catalog.QuoteIdentifier(s.owner) + ";\n"); err != nil {
				return err
			}
		}
	}
	for _, line := range script.conversionSchemas {
		if err := emit(line); err != nil {
			return err
		}
	}
	for _, line := range script.seedStatements {
		if err := emit(line); err != nil {
			return err
		}
	}
	for _, line := range script.conversionSeeds {
		if err := emit(line); err != nil {
			return err
		}
	}
	sort.Slice(script.schemaComments, func(i, j int) bool { return script.schemaComments[i].name < script.schemaComments[j].name })
	for _, c := range script.schemaComments {
		literal := "NULL"
		if c.text != "" {
			literal = dump.CommentLiteral(c.text)
		}
		if err := emit("COMMENT ON SCHEMA " + catalog.QuoteIdentifier(c.name) + " IS " + literal + ";\n"); err != nil {
			return err
		}
	}
	sequenceCreation, sequenceCompletion := sequence.DumpStatements(script.sequences)
	for _, line := range sequenceCreation {
		if err := emit(line); err != nil {
			return err
		}
	}
	currentOwner := ""
	for _, statements := range [2][]dump.Statement{script.collationStatements, script.conversionStatements} {
		for _, declaration := range statements {
			if declaration.Owner != currentOwner {
				if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(declaration.Owner, "'", "''") + "';\n"); err != nil {
					return err
				}
				currentOwner = declaration.Owner
			}
			if err := emit(declaration.SQL); err != nil {
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
	for _, event := range script.eventOrder {
		oid := event.OID
		if event.Phase != dump.Declaration {
			if event.Phase == dump.OperatorLinks {
				if err := setOwner("postgres"); err != nil {
					return err
				}
				if err := emit(script.operatorLinks[oid]); err != nil {
					return err
				}
				continue
			}
			if event.Phase == dump.DomainChecks {
				text, err := domainInvalidDumpStatement(ctx, script.catalogView, script.domainNodes[oid])
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
			if event.Phase == dump.TableCompletion {
				continue
			}
			if event.Phase == dump.InvalidIndexPreparation || event.Phase == dump.InvalidIndexCompletion {
				if err := setOwner("postgres"); err != nil {
					return err
				}
				text, err := script.invalidReplay[oid].sql(ctx, script.originalCatalog, event.Phase)
				if err != nil {
					return err
				}
				if err := emit(text); err != nil {
					return err
				}
				continue
			}
			if event.Phase == dump.Rename || event.Phase == dump.IndexAttachment || event.Phase == dump.TableAttachment {
				if err := setOwner("postgres"); err != nil {
					return err
				}
				descriptor := script.originalCatalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
				var text string
				switch event.Phase {
				case dump.Rename:
					text = script.constraintReplay.finalRenameSQL(descriptor)
				case dump.IndexAttachment:
					text = partitionIndexDumpAttachmentSQL(script.originalCatalog, descriptor)
				case dump.TableAttachment:
					text, err = partitionAttachDumpSQL(ctx, script.originalCatalog, descriptor)
					if err != nil {
						return err
					}
					text += script.constraintReplay.membershipRenames(script.catalogView, descriptor, event)
				}
				if err := emit(text); err != nil {
					return err
				}
				continue
			}
			t := script.tables[script.tableIndices[oid]]
			owner := t.owner
			if event.Phase == dump.InheritanceChecks || event.Phase == dump.RetiredConstraints || event.Phase == dump.InheritedForeignKeys || event.Phase == dump.InvalidConstraints && catalog.InheritanceParticipant(script.originalCatalog, t.descriptor) {
				owner = "postgres"
			}
			if err := setOwner(owner); err != nil {
				return err
			}
			switch event.Phase {
			case dump.Defaults:
				err = t.emitDeferredDefaults(ctx, script.originalCatalog, emit)
			case dump.Rows:
				err = t.emitRows(ctx, script.originalCatalog, emit)
			case dump.InvalidConstraints:
				err = t.emitInvalidConstraints(ctx, script.originalCatalog, emit)
			case dump.InheritanceRecipes:
				err = t.emitInheritedGenerations(ctx, script.originalCatalog, emit)
			case dump.InheritanceChecks:
				err = t.emitInheritedChecks(ctx, script.originalCatalog, emit)
			case dump.InheritedForeignKeys:
				err = t.emitInheritedForeignKeys(script.originalCatalog, emit)
			case dump.RetiredConstraints:
				err = t.emitRetiredConstraints(script.originalCatalog, false, emit, setOwner)
				if err == nil {
					err = t.emitRetiredConstraints(script.originalCatalog, true, emit, setOwner)
				}
			}
			if err != nil {
				return err
			}
			continue
		}
		if declaration, exists := script.declarations[oid]; exists {
			if domain := script.domainNodes[oid]; domain != nil && domain.SQLType.IsDomain() && declaration.Owner != "postgres" {
				// Owner metadata does not imply schema CREATE/USAGE. Preserve
				// its captured identity, construct as bootstrap, then transfer only
				// the exact fresh domain through the ordinary owner executor.
				if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(declaration.Owner, "'", "''") + "';\n"); err != nil {
					return err
				}
				currentOwner = declaration.Owner
				if err := setOwner("postgres"); err != nil {
					return err
				}
				if err := emit(declaration.SQL); err != nil {
					return err
				}
				qualified, err := catalogTypeDeclaration(script.catalogView, domain.SQLType.Usage())
				if err != nil {
					return err
				}
				if err := emit("ALTER DOMAIN " + qualified + " OWNER TO " + catalog.Identifier(declaration.Owner) + ";\n"); err != nil {
					return err
				}
				continue
			}
			if declaration.Owner != currentOwner {
				if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(declaration.Owner, "'", "''") + "';\n"); err != nil {
					return err
				}
				currentOwner = declaration.Owner
			}
			if err := emit(declaration.SQL); err != nil {
				return err
			}
			continue
		}
		if position, isIndex := script.indexIndices[oid]; isIndex {
			index := script.indexes[position]
			if index.owner != currentOwner {
				if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(index.owner, "'", "''") + "';\n"); err != nil {
					return err
				}
				currentOwner = index.owner
			}
			if err := emit(index.sql); err != nil {
				return err
			}
			original := script.originalCatalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
			if original.SQLIndex != nil && original.SQLIndex.Constraint != nil && original.SQLIndex.Constraint.Primary {
				parent := script.originalCatalog.MustRelation(original.SQLIndex.Parent)
				for i := range parent.Constraints.Unique {
					if parent.Constraints.Unique[i].Index == original.Address() {
						if err := emitPhysicalPrimaryCleanup(script.originalCatalog, parent, &parent.Constraints.Unique[i], emit, setOwner); err != nil {
							return err
						}
						if err := emitPhysicalSelfForeignKeys(script.originalCatalog, parent, index.owner, true, emit, setOwner); err != nil {
							return err
						}
						if err := emitPhysicalCompetingUniques(script.originalCatalog, parent, index.owner, true, emit, setOwner); err != nil {
							return err
						}
						break
					}
				}
			}
			if partitionReplayVirtualUniqueRoot(script.originalCatalog, original) != nil {
				if err := emit(script.constraintReplay.virtualUniqueRenames(script.originalCatalog, original)); err != nil {
					return err
				}
			}
			descriptor := script.catalogView.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
			if script.constraintReplay.occupied[descriptor.Namespace()] == nil {
				script.constraintReplay.occupied[descriptor.Namespace()] = make(map[string]bool)
			}
			script.constraintReplay.occupied[descriptor.Namespace()][descriptor.Name()] = true
			continue
		}
		if index, isView := script.viewIndices[oid]; isView {
			view := script.views[index]
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
		t := script.tables[script.tableIndices[oid]]
		t.descriptor = script.catalogView.MustRelation(t.descriptor.Address())
		inheritedOwnerReplay := t.descriptor.Inheritance != nil && t.owner != "postgres"
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
		qualified := catalog.QuoteIdentifier(t.schema) + "." + catalog.QuoteIdentifier(t.name)
		// CREATE TABLE OF cannot declare identity/generation or preserve a
		// post-creation array rank. Reconstruct ordinary columns, then attach.
		attachAfterCreate := t.descriptor.OfType != (ids.ObjectAddress{}) && (len(t.identities) != 0 || len(t.descriptor.ColumnMeta.Generations) != 0 || slices.ContainsFunc(t.columns, func(column catalog.Column) bool { return column.DeclaredDimensions != 0 }))
		var line strings.Builder
		if t.descriptor.Membership == nil || partitionDumpNeedsAttach(script.catalogView, t.descriptor) {
			typed := t.descriptor.OfType != (ids.ObjectAddress{}) && !attachAfterCreate
			hasElements := !typed || len(t.columns) != 0 || len(t.descriptor.Constraints.Check) != 0 || len(t.descriptor.Constraints.Unique) != 0 || len(t.descriptor.Constraints.Exclusion) != 0 || len(t.descriptor.Constraints.ForeignKeys) != 0
			line.WriteString("CREATE TABLE " + qualified)
			if typed {
				source := script.catalogView.MustType(t.descriptor.OfType)
				namespace := script.catalogView.Get(source.ObjectNamespace).Name()
				line.WriteString(" OF " + catalog.QuoteIdentifier(namespace) + "." + catalog.QuoteIdentifier(source.Name()))
			}
			if hasElements {
				line.WriteString(" (")
			}
			localElements := 0
			for i, c := range t.columns {
				provenance := catalog.InheritedColumn{Local: true, ExplicitDefault: true, ExplicitGeneration: true}
				if t.descriptor.Inheritance != nil {
					provenance = t.descriptor.Inheritance.Columns[t.attributes[i]]
				}
				if effective, exists := t.recipes[t.attributes[i]]; exists {
					provenance = effective
				}
				if !provenance.Local {
					continue
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				line.WriteString(catalog.QuoteIdentifier(c.Name))
				if typed {
					line.WriteString(" WITH OPTIONS")
				} else {
					declaration, err := catalogColumnDeclaration(script.catalogView, c)
					if err != nil {
						return err
					}
					line.WriteString(" " + declaration)
					if c.Usage.Collation.ID != c.Usage.Type.Usage().Collation.ID {
						name := typedef.CollationSQLName(script.catalogView, c.Usage.Collation.ID)
						if name == "" {
							return fmt.Errorf("stuntgres: unsupported stored collation")
						}
						line.WriteString(" COLLATE " + name)
					}
				}
				if definition, exists := t.defaults[t.attributes[i]]; exists && provenance.ExplicitDefault && !(script.deferDefaults && t.deferredDefinitions[t.attributes[i]]) {
					text, err := defaultSQL(ctx, script.catalogView, definition.Expression)
					if err != nil {
						return err
					}
					line.WriteString(" DEFAULT " + text)
				}
				if provenance.ExplicitDefault && t.descriptor.Inheritance != nil {
					if _, exists := t.defaults[t.attributes[i]]; !exists {
						line.WriteString(" DEFAULT NULL")
					}
				}
				if definition, exists := t.descriptor.ColumnMeta.Generations[t.attributes[i]]; exists && provenance.ExplicitGeneration {
					var text string
					if t.recipePlaceholders {
						// The genuine owning declaration supplies the full type/typmod.
						// An explicit cast would require unrelated public cast syntax.
						text = "NULL"
					} else {
						var err error
						text, err = (scalarRenderer{owner: t.descriptor}).sql(ctx, script.catalogView, definition.Expression)
						if err != nil {
							return err
						}
					}
					line.WriteString(" GENERATED ALWAYS AS (" + text + ") " + definition.Kind.SQL())
				}
				if identity, exists := t.identities[t.attributes[i]]; exists {
					mode := "ALWAYS"
					if identity.Mode == parser.IdentityByDefault {
						mode = "BY DEFAULT"
					}
					dumped := t.identitySequences[identity.Sequence]
					line.WriteString(" GENERATED " + mode + " AS IDENTITY (SEQUENCE NAME " + catalog.QuoteIdentifier(dumped.Schema) + "." + catalog.QuoteIdentifier(dumped.Name))
					if !sequence.IdentityDumpDivergent(dumped.Options, c.Usage.Type.ID()) {
						line.WriteString(" " + sequence.NumericSQL(dumped.Options))
					}
					line.WriteString(")")
				}
				if !c.Nullable && dumpNotNullDeclaration(script.originalCatalog, t.descriptor, t.attributes[i]) {
					for _, constraint := range t.notNullConstraints {
						if constraint.Attribute == t.attributes[i] && !constraint.NotValid {
							line.WriteString(" CONSTRAINT " + catalog.QuoteIdentifier(constraint.Name) + " NOT NULL")
							break
						}
					}
				}
			}
			if inheritance := t.descriptor.Inheritance; inheritance != nil {
				for _, constraint := range t.notNullConstraints {
					if constraint.NotValid {
						continue
					}
					if inheritance.Columns[constraint.Attribute].Local || !dumpNotNullDeclaration(script.originalCatalog, t.descriptor, constraint.Attribute) {
						continue
					}
					if localElements > 0 {
						line.WriteString(", ")
					}
					localElements++
					slot := t.descriptor.IndexForAttribute(constraint.Attribute)
					line.WriteString("CONSTRAINT " + catalog.QuoteIdentifier(constraint.Name) + " NOT NULL " + catalog.QuoteIdentifier(t.columns[slot].Name))
				}
			}
			for _, c := range orderedChecks(t.descriptor) {
				if c.NotValid || t.deferredChecks[c.Name] {
					continue
				}
				if !dumpCheckDeclaration(script.originalCatalog, t.descriptor, c) {
					continue
				}
				text, err := (scalarRenderer{owner: t.descriptor, checkReplayPrecision: true}).sql(ctx, script.catalogView, c.Expression)
				if err != nil {
					return err
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				line.WriteString("CONSTRAINT " + catalog.QuoteIdentifier(c.Name) + " CHECK (" + text + ")")
				if c.NoInherit {
					line.WriteString(" NO INHERIT")
				}
			}
			for _, c := range t.descriptor.Constraints.Unique {
				if physicalPrimaryDeferred(t.descriptor, &c) {
					continue
				}
				index, ok := script.catalogView.Relation(c.Index)
				if !ok {
					return fmt.Errorf("stuntgres: missing unique dump index")
				}
				if _, moved, err := physicalCompetingIndexSource(script.originalCatalog, index); err != nil {
					return err
				} else if moved {
					continue
				}
				if partitionReplaySeparateConstraint(script.catalogView, index) {
					continue
				}
				text, err := (pgRenderer{view: script.catalogView}).uniqueConstraintSQL(t.descriptor, &c)
				if err != nil {
					return err
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				line.WriteString("CONSTRAINT " + catalog.QuoteIdentifier(c.Name) + " " + text)
			}
			for _, c := range t.descriptor.Constraints.Exclusion {
				if c.Parent != 0 {
					continue
				}
				index, ok := script.catalogView.Relation(c.Index)
				if !ok {
					return fmt.Errorf("stuntgres: missing exclusion dump index")
				}
				if partitionReplaySeparateConstraint(script.catalogView, index) {
					continue
				}
				text, err := indexDefinitionSQL(ctx, script.catalogView, index, indexPresentation{dump: true, exclusion: true})
				if err != nil {
					return err
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				line.WriteString("CONSTRAINT " + catalog.QuoteIdentifier(c.Name) + " " + text)
			}
			for _, f := range t.descriptor.Constraints.ForeignKeys {
				if f.NotValid || physicalSelfForeignKeyDeferred(t.descriptor, f) || inheritedForeignKeyDeferred(script.originalCatalog, t.descriptor, &f) {
					continue
				}
				text, err := (pgRenderer{view: script.catalogView}).foreignKeyConstraintSQL(t.descriptor, f)
				if err != nil {
					return err
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				line.WriteString("CONSTRAINT " + catalog.QuoteIdentifier(f.Name) + " " + text)
			}
			if hasElements {
				line.WriteString(")")
			}
			if inheritance := t.descriptor.Inheritance; inheritance != nil {
				line.WriteString(" INHERITS (")
				for i, address := range inheritance.Parents {
					if i > 0 {
						line.WriteString(", ")
					}
					parent := script.catalogView.MustRelation(address)
					namespace := script.catalogView.Get(parent.Namespace()).Name()
					line.WriteString(catalog.QuoteIdentifier(namespace) + "." + catalog.QuoteIdentifier(parent.Name()))
				}
				line.WriteString(")")
			}
		} else {
			var deferred map[int32]bool
			if script.deferDefaults {
				deferred = t.deferredDefinitions
			}
			definition, err := partitionChildSQL(ctx, script.catalogView, t.descriptor, qualified, deferred)
			if err != nil {
				return err
			}
			line.WriteString(definition)
		}
		key, err := partitionKeySQL(ctx, script.catalogView, t.descriptor)
		if err != nil {
			return err
		}
		line.WriteString(key + ";\n")
		if err := emit(line.String()); err != nil {
			return err
		}
		if inheritedOwnerReplay {
			if err := emit("ALTER TABLE " + qualified + " OWNER TO " + catalog.QuoteIdentifier(t.owner) + ";\n"); err != nil {
				return err
			}
			if err := setOwner(t.owner); err != nil {
				return err
			}
		}
		if err := t.emitInheritedDefaults(ctx, script.originalCatalog, script.deferDefaults, emit); err != nil {
			return err
		}
		if err := t.emitNoInheritNotNull(emit, setOwner); err != nil {
			return err
		}
		if err := t.emitPhysicalNotNull(script.originalCatalog, emit, setOwner); err != nil {
			return err
		}
		if err := emitPhysicalSelfForeignKeys(script.originalCatalog, t.descriptor, t.owner, false, emit, setOwner); err != nil {
			return err
		}
		if err := emitPhysicalCompetingUniques(script.originalCatalog, t.descriptor, t.owner, false, emit, setOwner); err != nil {
			return err
		}
		if attachAfterCreate {
			source := script.catalogView.MustType(t.descriptor.OfType)
			namespace := script.catalogView.Get(source.ObjectNamespace).Name()
			if err := emit("ALTER TABLE " + qualified + " OF " + catalog.QuoteIdentifier(namespace) + "." + catalog.QuoteIdentifier(source.Name()) + ";\n"); err != nil {
				return err
			}
		}
		renames, err := script.constraintReplay.tableRenames(script.catalogView, t.descriptor)
		if err != nil {
			return err
		}
		if err := emit(renames); err != nil {
			return err
		}
		if t.comment != "" {
			if err := emit("COMMENT ON TABLE " + qualified + " IS " + dump.CommentLiteral(t.comment) + ";\n"); err != nil {
				return err
			}
		}
		for i, c := range t.columns {
			if text := t.columnComments[t.attributes[i]]; text != "" {
				if err := emit("COMMENT ON COLUMN " + qualified + "." + catalog.QuoteIdentifier(c.Name) + " IS " + dump.CommentLiteral(text) + ";\n"); err != nil {
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
	slices.SortFunc(script.materialized, func(a, b dumpRelation) int {
		if order := strings.Compare(a.schema, b.schema); order != 0 {
			return order
		}
		return strings.Compare(a.name, b.name)
	})
	for i := range script.materialized {
		if err := script.materialized[i].emitMaterializedSnapshot(ctx, script.originalCatalog, emit); err != nil {
			return err
		}
	}
	for _, group := range script.policyGroups {
		if group.Owner != currentOwner {
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(group.Owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = group.Owner
		}
		if err := emit(group.SQL); err != nil {
			return err
		}
	}
	for _, group := range script.securityGroups {
		if group.Owner != currentOwner {
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(group.Owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = group.Owner
		}
		if err := emit(group.SQL); err != nil {
			return err
		}
	}
	// User rules are post-data: importing ordinary INSERT rows must not rewrite
	// fixture data before every relation and original stored row exists.
	for _, group := range script.ruleGroups {
		if group.Owner != currentOwner {
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(group.Owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = group.Owner
		}
		if err := emit(group.SQL); err != nil {
			return err
		}
	}
	// INHERITS reconstructs every inherited/merged declaration with rank zero.
	// Repair only the lost metadata after all owner SQL, as genuine PostgreSQL
	// catalog SQL under the authenticated administrator, not as replay comments.
	rankAuthority := false
	for _, event := range script.eventOrder {
		index, exists := script.tableIndices[event.OID]
		if event.Phase != dump.Declaration || !exists {
			continue
		}
		table := script.tables[index]
		inheritance := table.descriptor.Inheritance
		if inheritance == nil {
			continue
		}
		for i, column := range table.columns {
			if column.DeclaredDimensions == 0 || len(inheritance.Columns[table.attributes[i]].Origins) == 0 {
				continue
			}
			if !rankAuthority {
				if err := emit("SET SESSION AUTHORIZATION DEFAULT;\n"); err != nil {
					return err
				}
				rankAuthority = true
			}
			qualified := catalog.QuoteIdentifier(script.catalogView.Get(table.descriptor.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.name)
			if err := emit(fmt.Sprintf("UPDATE pg_catalog.pg_attribute SET attndims=%d WHERE attrelid=%s::regclass AND attname=%s;\n", column.DeclaredDimensions, dump.CommentLiteral(qualified), dump.CommentLiteral(column.Name))); err != nil {
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
	var extensionOwners []extension.OwnerRecord
	var domainReplays []typedef.PositionedDomainReplay
	statements, err := parser.ParseWithOptions(string(data), parser.Options{Context: ctx, StandardConformingStrings: true, LineComment: func(comment string, position parser.Position) error {
		domainRecord, domainRecognized, domainErr := typedef.DecodeDomainReplay(comment)
		if domainErr != nil {
			return domainErr
		}
		if domainRecognized {
			domainReplays = append(domainReplays, typedef.PositionedDomainReplay{Record: domainRecord, End: position.Offset + len(comment)})
			return nil
		}
		ownerRecord, ownerRecognized, ownerErr := extension.DecodeOwners(comment)
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
	domainReplayPairs, err := typedef.PairDomainReplay(string(data), statements, domainReplays)
	if err != nil {
		return err
	}
	// The isolated role snapshot retains exact target identities for final remapping.
	sess, err := stage.StartSession(ctx, session.SessionStart{ID: 1, Authenticated: stage.roles.domain.Principal(10)})
	if err != nil {
		return err
	}
	defer func() { sess.RequestEnd(session.CleanClose); _ = sess.WaitEnd(context.Background()) }()
	op, err := sess.Acquire(ctx)
	if err != nil {
		return err
	}
	defer op.Done()
	defer func() {
		operationReplay(op).sqlBodies = false
		operationReplay(op).defaultReplay = false
		operationReplay(op).partitionAttach = false
		operationReplay(op).partitionCheck = nil
		operationReplay(op).partitionUsingIndex = nil
		operationReplay(op).partitionUnique = nil
	}()
	var pendingForeignKeys []restoredForeignKeys
	partitionTeardowns := make(map[ids.ObjectAddress]partitionReplayTeardown)
	notNullOrigins := make(map[ids.OID]restoredNotNull)
	var pairedNotNull restoredNotNull
	var pairedPhysical *restoredPhysicalNotNull
	var deferredPhysical *restoredPhysicalNotNull
	physicalIndexes := make(map[ids.ObjectAddress]*catalog.Relation)
	var physicalForeignProvider *restoredPhysicalForeignKeyProvider
	placeholderOrigins := make(map[restoredGenerationKey]restoredGenerationRecipe)
	var generationRepairs []restoredGenerationRecipe
	var lastAuthorization, createOwner authorization.PrincipalRef
	var pairedOwner restoredInheritedOwner
	var pairedDomainOwner typedef.RestoredDomainOwner
	historicalRefs := make(map[ids.ObjectAddress]ids.ObjectRef)
	historicalAuthority := &restoredHistoryAuthority{facts: make(map[ids.OID]restoredHistoricalFact), archives: make(map[uint64]uint64), sources: make(map[retiredSourceIdentity]retiredSourceDescription), nextArchive: e.retiredArchiveHighWater()}
	for statementIndex, stmt := range statements {
		if routine.ReplayDirective(string(data), stmt) {
			operationReplay(op).sqlBodies = true
			continue
		}
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
		var physicalIndexBase catalog.Read
		if _, ok := stmt.(*parser.CreateIndex); ok {
			physicalIndexBase = stage.CatalogRead()
		}
		priorAuthorization, ownerForCreate, ownerPair := lastAuthorization, createOwner, pairedOwner
		domainOwnerPair := pairedDomainOwner
		pairedDomainOwner = typedef.RestoredDomainOwner{}
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
						if err := dump.RestoreScalar(constraint.Default, true); err != nil {
							return err
						}
						if constraint.Generation != nil {
							if err := dump.RestoreScalar(constraint.Generation.Expression, true); err != nil {
								return err
							}
						}
						if constraint.Check != nil {
							if err := dump.RestoreScalar(constraint.Check.Expression, true); err != nil {
								return err
							}
						}
					}
				}
				if element.Check != nil {
					if err := dump.RestoreScalar(element.Check.Expression, true); err != nil {
						return err
					}
				}
				if element.Exclusion != nil {
					for _, key := range element.Exclusion.Index.Keys {
						if err := dump.RestoreScalar(key.Expression, true); err != nil {
							return err
						}
					}
					if err := dump.RestoreScalar(element.Exclusion.Index.Predicate, true); err != nil {
						return err
					}
				}
			}
		case *parser.AlterDomain:
			if err := restoreDomainAlter(s); err != nil {
				return err
			}
		case *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.CreateEnum, *parser.CreateDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.CreateConversion, *parser.AlterConversion:
		case *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator:
		case *parser.CreateFunction:
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
			if record.child.Domain() != nil {
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
						if err := dump.RestoreScalar(datum.Expression, true); err != nil {
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
			if !sequence.RestoresState(s) {
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
						if err := dump.RestoreScalar(bound.Lower, false); err != nil {
							return err
						}
					}
					if bound.Upper != nil {
						if err := dump.RestoreScalar(bound.Upper, false); err != nil {
							return err
						}
					}
				}
			}
			for _, row := range s.Rows {
				for _, v := range row {
					if err := dump.RestoreScalar(v, false); err != nil {
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
		operationReplay(op).tableDestination = false
		if _, ok := stmt.(*parser.CreateTable); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateDomain); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateEnum); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateComposite); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreatePolicy); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateRule); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateSequence); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateView); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateIndex); ok {
			operationReplay(op).tableDestination = true
		}
		if creation, ok := stmt.(*parser.CreateQueryTable); ok && creation.Form == parser.CreateMaterializedView {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateCollation); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateConversion); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.AlterConversion); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateOperator); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.AlterOperator); ok {
			operationReplay(op).tableDestination = true
		}
		if _, ok := stmt.(*parser.CreateExtension); ok {
			operationReplay(op).tableDestination = true
		}
		operationReplay(op).insertAuthority = false
		if _, ok := stmt.(*parser.Insert); ok {
			operationReplay(op).insertAuthority = true
		}
		operationReplay(op).commentAuthority = false
		if _, ok := stmt.(*parser.Comment); ok {
			operationReplay(op).commentAuthority = true
		}
		_, replayDefault := stmt.(*parser.AlterTableDefault)
		operationReplay(op).defaultReplay = replayDefault
		_, operationReplay(op).partitionAttach = stmt.(*parser.AlterTableAttachPartition)
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
			if len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.AddUnique && operationReplayOrZero(op).partitionUnique == nil && physicalConstructor == nil {
				return sqlError("0A000", "restore accepts only captured private virtual unique definitions")
			}
		}
		var result Result
		if insertion, ok := stmt.(*parser.Insert); ok {
			if record, paired := domainReplayPairs[insertion]; paired {
				result, err = stage.executeDomainReplayInsert(op, insertion, record)
			} else {
				result, err = stage.Execute(op, stmt, 0, nil)
			}
		} else {
			result, err = stage.Execute(op, stmt, 0, nil)
		}
		operationReplay(op).defaultReplay = false
		operationReplay(op).partitionAttach = false
		operationReplay(op).partitionCheck = nil
		operationReplay(op).partitionUsingIndex = nil
		operationReplay(op).partitionUnique = nil
		if result.Rows != nil {
			closeErr := result.Rows.Close()
			if err == nil {
				err = closeErr
			}
		}
		operationReplay(op).commentAuthority = false
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
				physicalIndexes[index.Address()] = index
			}
		}
		if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default {
			auth, authErr := operationAuthorization(op)
			if authErr != nil {
				return authErr
			}
			actor, roleErr := auth.Roles.Descriptor(auth.State.CurrentUser)
			if roleErr != nil {
				return roleErr
			}
			if actor.Superuser && priorAuthorization != (authorization.PrincipalRef{}) && priorAuthorization != actor.Ref {
				createOwner = priorAuthorization
			}
			lastAuthorization = actor.Ref
		}
		if _, domain := stmt.(*parser.CreateDomain); domain && ownerForCreate != (authorization.PrincipalRef{}) && result.created != (ids.ObjectRef{}) {
			pairedDomainOwner, err = stage.captureRestoredDomainOwner(op, result.created, ownerForCreate)
			if err != nil {
				return err
			}
		}
		if creation, ok := stmt.(*parser.CreateTable); ok && result.created != (ids.ObjectRef{}) {
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
		if result.created != (ids.ObjectRef{}) && len(pending.definitions) != 0 {
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
	if err := stage.validateRestoredSQLRoutines(ctx, op); err != nil {
		return err
	}
	_, err = e.replaceCatalog(ctx, stage, stage.CatalogRead(), op)
	return err
}
