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
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// emitDumpDeclaration writes one declaration event. A captured statement
// wins, then an index, a view or extension, and finally a table.
func emitDumpDeclaration(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	oid := event.OID
	if s.rawFloat4 {
		if final, ok := s.CatalogView.Cast(sqltype.Xid, sqltype.Float4); ok && final.Method != "b" && final.OID == oid {
			return nil
		}
		if final, ok := s.CatalogView.Cast(sqltype.Xid, sqltype.Float4); ok && final.Method == "b" && final.OID == oid && s.rawHelper {
			if err := w.setOwner("postgres"); err != nil {
				return err
			}
			if err := w.emit("DROP CAST (pg_catalog.xid AS pg_catalog.float4);\n"); err != nil {
				return err
			}
			s.rawHelper = false
		}
	}
	if declaration, exists := s.declarations[oid]; exists {
		return emitDumpStatement(s, w, oid, declaration)
	}
	if position, isIndex := s.IndexIndices[oid]; isIndex {
		return emitDumpIndex(s, w, oid, s.Indexes[position])
	}
	if index, isView := s.ViewIndices[oid]; isView {
		view := s.Views[index]
		if err := w.setOwner(view.Owner); err != nil {
			return err
		}
		return s.emitBound(w, castConsumer{Event: event}, view.SQL)
	}
	return emitDumpTable(ctx, s, w, s.table(oid))
}

// emitDumpStatement writes a domain, composite, routine or operator
// declaration under its owner.
func emitDumpStatement(s *dumpScript, w *dumpWriter, oid ids.OID, declaration dump.Statement) error {
	declaration.Binding = s.comparisonBindings[castConsumer{Event: dump.Event{OID: oid}}]
	if domain := s.domainNodes[oid]; domain != nil && domain.SQLType.IsDomain() && declaration.Owner != "postgres" {
		// Owner metadata does not imply schema CREATE/USAGE. Preserve
		// its captured identity, construct as bootstrap, then transfer only
		// the exact fresh domain through the ordinary owner executor.
		if err := w.forceOwner(declaration.Owner); err != nil {
			return err
		}
		if err := w.setOwner("postgres"); err != nil {
			return err
		}
		if err := w.emitDefinition(s.CatalogView, declaration); err != nil {
			return err
		}
		qualified, err := regobj.TypeDeclaration(s.CatalogView, domain.SQLType.Usage())
		if err != nil {
			return err
		}
		return w.emit("ALTER DOMAIN " + qualified + " OWNER TO " + catalog.Identifier(declaration.Owner) + ";\n")
	}
	if err := w.setOwner(declaration.Owner); err != nil {
		return err
	}
	return w.emitDefinition(s.CatalogView, declaration)
}

// emitDumpIndex writes an index definition, the physical primary-key
// cleanup that follows it, and records its name as occupied.
func emitDumpIndex(s *dumpScript, w *dumpWriter, oid ids.OID, idx OwnedStatement) error {
	if err := w.setOwner(idx.Owner); err != nil {
		return err
	}
	if err := s.emitBound(w, castConsumer{Event: dump.Event{OID: oid}}, idx.SQL); err != nil {
		return err
	}
	original := s.OriginalCatalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
	if original.SQLIndex != nil && original.SQLIndex.Constraint != nil && original.SQLIndex.Constraint.Primary {
		parent := s.OriginalCatalog.MustRelation(original.SQLIndex.Parent)
		for i := range parent.Constraints.Unique {
			if parent.Constraints.Unique[i].Index == original.Address() {
				if err := table.EmitPhysicalPrimaryCleanup(s.OriginalCatalog, parent, &parent.Constraints.Unique[i], w.emit, w.setOwner); err != nil {
					return err
				}
				if err := table.EmitPhysicalSelfForeignKeys(s.OriginalCatalog, parent, idx.Owner, true, w.emit, w.setOwner); err != nil {
					return err
				}
				if err := table.EmitPhysicalCompetingUniques(s.OriginalCatalog, parent, idx.Owner, true, w.emit, w.setOwner); err != nil {
					return err
				}
				break
			}
		}
	}
	if index.PartitionReplayVirtualUniqueRoot(s.OriginalCatalog, original) != nil {
		if err := w.emit(s.ConstraintReplay.VirtualUniqueRenames(s.OriginalCatalog, original)); err != nil {
			return err
		}
	}
	descriptor := s.CatalogView.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
	if s.ConstraintReplay.Occupied[descriptor.Namespace()] == nil {
		s.ConstraintReplay.Occupied[descriptor.Namespace()] = make(map[string]bool)
	}
	s.ConstraintReplay.Occupied[descriptor.Namespace()][descriptor.Name()] = true
	return nil
}

// emitDumpTable writes a table's CREATE and the statements that complete
// its declaration: owner transfer, inherited defaults, NOT NULL and key
// repairs, typed attachment, renames and comments.
func emitDumpTable(ctx context.Context, s *dumpScript, w *dumpWriter, t table.DumpRelation) error {
	t.Descriptor = s.CatalogView.MustRelation(t.Descriptor.Address())
	inheritedOwnerReplay := t.Descriptor.Inheritance != nil && t.Owner != "postgres"
	if inheritedOwnerReplay {
		// Retain a real captured-role directive before the privileged CREATE;
		// the isolated importer pairs this exact new relation and role.
		if err := w.forceOwner(t.Owner); err != nil {
			return err
		}
		if err := w.setOwner("postgres"); err != nil {
			return err
		}
	} else if err := w.setOwner(t.Owner); err != nil {
		return err
	}
	qualified := catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name)
	// CREATE TABLE OF cannot declare identity/generation or preserve a
	// post-creation array rank. Reconstruct ordinary columns, then attach.
	attachAfterCreate := t.Descriptor.OfType != (ids.ObjectAddress{}) && (len(t.Identities) != 0 || len(t.Descriptor.ColumnMeta.Generations) != 0 || slices.ContainsFunc(t.Columns, func(column catalog.Column) bool { return column.DeclaredDimensions != 0 }))
	text, err := dumpTableCreateSQL(ctx, s, &t, attachAfterCreate)
	if err != nil {
		return err
	}
	if err := s.emitBound(w, castConsumer{Event: dump.Event{OID: t.Descriptor.Address().ObjectID}}, text); err != nil {
		return err
	}
	if inheritedOwnerReplay {
		if err := w.emit("ALTER TABLE " + qualified + " OWNER TO " + catalog.QuoteIdentifier(t.Owner) + ";\n"); err != nil {
			return err
		}
		if err := w.setOwner(t.Owner); err != nil {
			return err
		}
	}
	if err := w.emit(dumpRelationOptionsSQL(qualified, t.Descriptor.StorageOptions)); err != nil {
		return err
	}
	if err := t.EmitInheritedDefaults(ctx, s.OriginalCatalog, s.DeferDefaults, func(attribute int32, sql string) error {
		return s.emitBound(w, castConsumer{Event: dump.Event{OID: t.Descriptor.Address().ObjectID}, Attribute: attribute}, sql)
	}); err != nil {
		return err
	}
	if err := t.EmitNoInheritNotNull(w.emit, w.setOwner); err != nil {
		return err
	}
	if err := t.EmitPhysicalNotNull(s.OriginalCatalog, w.emit, w.setOwner); err != nil {
		return err
	}
	if err := table.EmitPhysicalSelfForeignKeys(s.OriginalCatalog, t.Descriptor, t.Owner, false, w.emit, w.setOwner); err != nil {
		return err
	}
	if err := table.EmitPhysicalCompetingUniques(s.OriginalCatalog, t.Descriptor, t.Owner, false, w.emit, w.setOwner); err != nil {
		return err
	}
	if attachAfterCreate {
		source := s.CatalogView.MustType(t.Descriptor.OfType)
		namespace := s.CatalogView.Get(source.ObjectNamespace).Name()
		if err := w.emit("ALTER TABLE " + qualified + " OF " + catalog.QuoteIdentifier(namespace) + "." + catalog.QuoteIdentifier(source.Name()) + ";\n"); err != nil {
			return err
		}
	}
	renames, err := s.ConstraintReplay.TableRenames(s.CatalogView, t.Descriptor)
	if err != nil {
		return err
	}
	if err := w.emit(renames); err != nil {
		return err
	}
	if t.Comment != "" {
		if err := w.emit("COMMENT ON TABLE " + qualified + " IS " + dump.CommentLiteral(t.Comment) + ";\n"); err != nil {
			return err
		}
	}
	for i, c := range t.Columns {
		if text := t.ColumnComments[t.Attributes[i]]; text != "" {
			if err := w.emit("COMMENT ON COLUMN " + qualified + "." + catalog.QuoteIdentifier(c.Name) + " IS " + dump.CommentLiteral(text) + ";\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

// dumpRelationOptionsSQL renders captured explicit intent with base entries
// followed by retained TOAST entries. Callers retain the declaration's owner.
func dumpRelationOptionsSQL(qualified string, options catalog.RelationOptions) string {
	if len(options.Base)+len(options.Toast) == 0 {
		return ""
	}
	var line strings.Builder
	line.WriteString("ALTER TABLE ")
	line.WriteString(qualified)
	line.WriteString(" SET (")
	for i, option := range options.Base {
		if i != 0 {
			line.WriteString(", ")
		}
		line.WriteString(catalog.QuoteIdentifier(option.Name))
		line.WriteString(" = ")
		line.WriteString(dump.CommentLiteral(option.Text))
	}
	for i, option := range options.Toast {
		if len(options.Base) != 0 || i != 0 {
			line.WriteString(", ")
		}
		line.WriteString(`"toast".`)
		line.WriteString(catalog.QuoteIdentifier(option.Name))
		line.WriteString(" = ")
		line.WriteString(dump.CommentLiteral(option.Text))
	}
	line.WriteString(");\n")
	return line.String()
}

// dumpTableCreateSQL renders a table's CREATE TABLE (or partition child)
// statement. It writes nothing, so every rendering error precedes output.
func dumpTableCreateSQL(ctx context.Context, s *dumpScript, t *table.DumpRelation, attachAfterCreate bool) (string, error) {
	qualified := catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name)
	var line strings.Builder
	if t.Descriptor.Membership == nil || partition.DumpNeedsAttach(s.CatalogView, t.Descriptor) {
		typed := t.Descriptor.OfType != (ids.ObjectAddress{}) && !attachAfterCreate
		hasElements := !typed || len(t.Columns) != 0 || len(t.Descriptor.Constraints.Check) != 0 || len(t.Descriptor.Constraints.Unique) != 0 || len(t.Descriptor.Constraints.Exclusion) != 0 || len(t.Descriptor.Constraints.ForeignKeys) != 0
		line.WriteString("CREATE TABLE " + qualified)
		if typed {
			source := s.CatalogView.MustType(t.Descriptor.OfType)
			namespace := s.CatalogView.Get(source.ObjectNamespace).Name()
			line.WriteString(" OF " + catalog.QuoteIdentifier(namespace) + "." + catalog.QuoteIdentifier(source.Name()))
		}
		if hasElements {
			line.WriteString(" (")
		}
		localElements := 0
		for i, c := range t.Columns {
			provenance := t.ColumnProvenance(t.Attributes[i])
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
				declaration, err := regobj.ColumnDeclaration(s.CatalogView, c)
				if err != nil {
					return "", err
				}
				line.WriteString(" " + declaration)
				if c.Usage.Collation.ID != c.Usage.Type.Usage().Collation.ID {
					name := typedef.CollationSQLName(s.CatalogView, c.Usage.Collation.ID)
					if name == "" {
						return "", fmt.Errorf("stuntgres: unsupported stored collation")
					}
					line.WriteString(" COLLATE " + name)
				}
			}
			if definition, exists := t.Defaults[t.Attributes[i]]; exists && provenance.ExplicitDefault && !(s.DeferDefaults && t.DeferredDefinitions[t.Attributes[i]]) {
				text, err := stored.DefaultSQL(ctx, s.CatalogView, definition.Expression)
				if err != nil {
					return "", err
				}
				line.WriteString(" DEFAULT " + text)
			}
			if provenance.ExplicitDefault && t.Descriptor.Inheritance != nil {
				if _, exists := t.Defaults[t.Attributes[i]]; !exists {
					line.WriteString(" DEFAULT NULL")
				}
			}
			if definition, exists := t.Descriptor.ColumnMeta.Generations[t.Attributes[i]]; exists && provenance.ExplicitGeneration {
				var text string
				if t.RecipePlaceholders {
					// The genuine owning declaration supplies the full type/typmod.
					// An explicit cast would require unrelated public cast syntax.
					text = "NULL"
				} else {
					var err error
					text, err = (stored.ScalarRenderer{Owner: t.Descriptor}).SQL(ctx, s.CatalogView, definition.Expression)
					if err != nil {
						return "", err
					}
				}
				line.WriteString(" GENERATED ALWAYS AS (" + text + ") " + definition.Kind.SQL())
			}
			if identity, exists := t.Identities[t.Attributes[i]]; exists {
				mode := "ALWAYS"
				if identity.Mode == parser.IdentityByDefault {
					mode = "BY DEFAULT"
				}
				dumped := t.IdentitySequences[identity.Sequence]
				line.WriteString(" GENERATED " + mode + " AS IDENTITY (SEQUENCE NAME " + catalog.QuoteIdentifier(dumped.Schema) + "." + catalog.QuoteIdentifier(dumped.Name))
				if !sequence.IdentityDumpDivergent(dumped.Options, c.Usage.Type.ID()) {
					line.WriteString(" " + sequence.NumericSQL(dumped.Options))
				}
				line.WriteString(")")
			}
			if !c.Nullable && table.DumpNotNullDeclaration(s.OriginalCatalog, t.Descriptor, t.Attributes[i]) {
				for _, constraint := range t.NotNullConstraints {
					if constraint.Attribute == t.Attributes[i] && !constraint.NotValid {
						line.WriteString(" CONSTRAINT " + catalog.QuoteIdentifier(constraint.Name) + " NOT NULL")
						break
					}
				}
			}
		}
		if inheritance := t.Descriptor.Inheritance; inheritance != nil {
			for _, constraint := range t.NotNullConstraints {
				if constraint.NotValid {
					continue
				}
				if inheritance.Columns[constraint.Attribute].Local || !table.DumpNotNullDeclaration(s.OriginalCatalog, t.Descriptor, constraint.Attribute) {
					continue
				}
				if localElements > 0 {
					line.WriteString(", ")
				}
				localElements++
				slot := t.Descriptor.IndexForAttribute(constraint.Attribute)
				line.WriteString("CONSTRAINT " + catalog.QuoteIdentifier(constraint.Name) + " NOT NULL " + catalog.QuoteIdentifier(t.Columns[slot].Name))
			}
		}
		for _, c := range table.OrderedChecks(t.Descriptor) {
			if c.NotValid || t.DeferredChecks[c.Name] {
				continue
			}
			if !table.DumpCheckDeclaration(s.OriginalCatalog, t.Descriptor, c) {
				continue
			}
			text, err := (stored.ScalarRenderer{Owner: t.Descriptor, CheckReplayPrecision: true}).SQL(ctx, s.CatalogView, c.Expression)
			if err != nil {
				return "", err
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
		for _, c := range t.Descriptor.Constraints.Unique {
			if table.PhysicalPrimaryDeferred(t.Descriptor, &c) {
				continue
			}
			idx, ok := s.CatalogView.Relation(c.Index)
			if !ok {
				return "", fmt.Errorf("stuntgres: missing unique dump index")
			}
			if _, moved, err := table.PhysicalCompetingIndexSource(s.OriginalCatalog, idx); err != nil {
				return "", err
			} else if moved {
				continue
			}
			if index.PartitionReplaySeparateConstraint(s.CatalogView, idx) {
				continue
			}
			text, err := (stored.Deparser{View: s.CatalogView}).UniqueConstraintSQL(t.Descriptor, &c)
			if err != nil {
				return "", err
			}
			if localElements > 0 {
				line.WriteString(", ")
			}
			localElements++
			line.WriteString("CONSTRAINT " + catalog.QuoteIdentifier(c.Name) + " " + text)
		}
		for _, c := range t.Descriptor.Constraints.Exclusion {
			if c.Parent != 0 {
				continue
			}
			idx, ok := s.CatalogView.Relation(c.Index)
			if !ok {
				return "", fmt.Errorf("stuntgres: missing exclusion dump index")
			}
			if index.PartitionReplaySeparateConstraint(s.CatalogView, idx) {
				continue
			}
			text, err := index.DefinitionSQL(ctx, s.CatalogView, idx, index.Presentation{Dump: true, Exclusion: true})
			if err != nil {
				return "", err
			}
			if localElements > 0 {
				line.WriteString(", ")
			}
			localElements++
			line.WriteString("CONSTRAINT " + catalog.QuoteIdentifier(c.Name) + " " + text)
		}
		for _, f := range t.Descriptor.Constraints.ForeignKeys {
			if f.NotValid || table.PhysicalSelfForeignKeyDeferred(t.Descriptor, f) || table.InheritedForeignKeyDeferred(s.OriginalCatalog, t.Descriptor, &f) {
				continue
			}
			text, err := (stored.Deparser{View: s.CatalogView}).ForeignKeyConstraintSQL(t.Descriptor, f)
			if err != nil {
				return "", err
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
		if inheritance := t.Descriptor.Inheritance; inheritance != nil && len(inheritance.Parents) != 0 {
			line.WriteString(" INHERITS (")
			for i, edge := range inheritance.Parents {
				address := edge.Relation
				if i > 0 {
					line.WriteString(", ")
				}
				parent := s.CatalogView.MustRelation(address)
				namespace := s.CatalogView.Get(parent.Namespace()).Name()
				line.WriteString(catalog.QuoteIdentifier(namespace) + "." + catalog.QuoteIdentifier(parent.Name()))
			}
			line.WriteString(")")
		}
	} else {
		var deferred map[int32]bool
		if s.DeferDefaults {
			deferred = t.DeferredDefinitions
		}
		definition, err := partition.ChildSQL(ctx, s.CatalogView, t.Descriptor, qualified, deferred)
		if err != nil {
			return "", err
		}
		line.WriteString(definition)
	}
	key, err := partition.KeySQL(ctx, s.CatalogView, t.Descriptor)
	if err != nil {
		return "", err
	}
	line.WriteString(key + ";\n")
	return line.String(), nil
}
