package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// emitDumpDeclaration writes one declaration event. A captured statement
// wins, then an index, a view or extension, and finally a table.
func emitDumpDeclaration(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	oid := event.OID
	if declaration, exists := s.declarations[oid]; exists {
		return emitDumpStatement(s, w, oid, declaration)
	}
	if position, isIndex := s.indexIndices[oid]; isIndex {
		return emitDumpIndex(s, w, oid, s.indexes[position])
	}
	if index, isView := s.viewIndices[oid]; isView {
		view := s.views[index]
		if err := w.setOwner(view.owner); err != nil {
			return err
		}
		return w.emit(view.sql)
	}
	return emitDumpTable(ctx, s, w, s.table(oid))
}

// emitDumpStatement writes a domain, composite, routine or operator
// declaration under its owner.
func emitDumpStatement(s *dumpScript, w *dumpWriter, oid ids.OID, declaration dump.Statement) error {
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
		if err := w.emit(declaration.SQL); err != nil {
			return err
		}
		qualified, err := catalogTypeDeclaration(s.catalogView, domain.SQLType.Usage())
		if err != nil {
			return err
		}
		return w.emit("ALTER DOMAIN " + qualified + " OWNER TO " + catalog.Identifier(declaration.Owner) + ";\n")
	}
	if err := w.setOwner(declaration.Owner); err != nil {
		return err
	}
	return w.emit(declaration.SQL)
}

// emitDumpIndex writes an index definition, the physical primary-key
// cleanup that follows it, and records its name as occupied.
func emitDumpIndex(s *dumpScript, w *dumpWriter, oid ids.OID, index dumpView) error {
	if err := w.setOwner(index.owner); err != nil {
		return err
	}
	if err := w.emit(index.sql); err != nil {
		return err
	}
	original := s.originalCatalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
	if original.SQLIndex != nil && original.SQLIndex.Constraint != nil && original.SQLIndex.Constraint.Primary {
		parent := s.originalCatalog.MustRelation(original.SQLIndex.Parent)
		for i := range parent.Constraints.Unique {
			if parent.Constraints.Unique[i].Index == original.Address() {
				if err := emitPhysicalPrimaryCleanup(s.originalCatalog, parent, &parent.Constraints.Unique[i], w.emit, w.setOwner); err != nil {
					return err
				}
				if err := emitPhysicalSelfForeignKeys(s.originalCatalog, parent, index.owner, true, w.emit, w.setOwner); err != nil {
					return err
				}
				if err := emitPhysicalCompetingUniques(s.originalCatalog, parent, index.owner, true, w.emit, w.setOwner); err != nil {
					return err
				}
				break
			}
		}
	}
	if partitionReplayVirtualUniqueRoot(s.originalCatalog, original) != nil {
		if err := w.emit(s.constraintReplay.virtualUniqueRenames(s.originalCatalog, original)); err != nil {
			return err
		}
	}
	descriptor := s.catalogView.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
	if s.constraintReplay.occupied[descriptor.Namespace()] == nil {
		s.constraintReplay.occupied[descriptor.Namespace()] = make(map[string]bool)
	}
	s.constraintReplay.occupied[descriptor.Namespace()][descriptor.Name()] = true
	return nil
}

// emitDumpTable writes a table's CREATE and the statements that complete
// its declaration: owner transfer, inherited defaults, NOT NULL and key
// repairs, typed attachment, renames and comments.
func emitDumpTable(ctx context.Context, s *dumpScript, w *dumpWriter, t dumpRelation) error {
	t.descriptor = s.catalogView.MustRelation(t.descriptor.Address())
	inheritedOwnerReplay := t.descriptor.Inheritance != nil && t.owner != "postgres"
	if inheritedOwnerReplay {
		// Retain a real captured-role directive before the privileged CREATE;
		// the isolated importer pairs this exact new relation and role.
		if err := w.forceOwner(t.owner); err != nil {
			return err
		}
		if err := w.setOwner("postgres"); err != nil {
			return err
		}
	} else if err := w.setOwner(t.owner); err != nil {
		return err
	}
	qualified := catalog.QuoteIdentifier(t.schema) + "." + catalog.QuoteIdentifier(t.name)
	// CREATE TABLE OF cannot declare identity/generation or preserve a
	// post-creation array rank. Reconstruct ordinary columns, then attach.
	attachAfterCreate := t.descriptor.OfType != (ids.ObjectAddress{}) && (len(t.identities) != 0 || len(t.descriptor.ColumnMeta.Generations) != 0 || slices.ContainsFunc(t.columns, func(column catalog.Column) bool { return column.DeclaredDimensions != 0 }))
	text, err := dumpTableCreateSQL(ctx, s, &t, attachAfterCreate)
	if err != nil {
		return err
	}
	if err := w.emit(text); err != nil {
		return err
	}
	if inheritedOwnerReplay {
		if err := w.emit("ALTER TABLE " + qualified + " OWNER TO " + catalog.QuoteIdentifier(t.owner) + ";\n"); err != nil {
			return err
		}
		if err := w.setOwner(t.owner); err != nil {
			return err
		}
	}
	if err := t.emitInheritedDefaults(ctx, s.originalCatalog, s.deferDefaults, w.emit); err != nil {
		return err
	}
	if err := t.emitNoInheritNotNull(w.emit, w.setOwner); err != nil {
		return err
	}
	if err := t.emitPhysicalNotNull(s.originalCatalog, w.emit, w.setOwner); err != nil {
		return err
	}
	if err := emitPhysicalSelfForeignKeys(s.originalCatalog, t.descriptor, t.owner, false, w.emit, w.setOwner); err != nil {
		return err
	}
	if err := emitPhysicalCompetingUniques(s.originalCatalog, t.descriptor, t.owner, false, w.emit, w.setOwner); err != nil {
		return err
	}
	if attachAfterCreate {
		source := s.catalogView.MustType(t.descriptor.OfType)
		namespace := s.catalogView.Get(source.ObjectNamespace).Name()
		if err := w.emit("ALTER TABLE " + qualified + " OF " + catalog.QuoteIdentifier(namespace) + "." + catalog.QuoteIdentifier(source.Name()) + ";\n"); err != nil {
			return err
		}
	}
	renames, err := s.constraintReplay.tableRenames(s.catalogView, t.descriptor)
	if err != nil {
		return err
	}
	if err := w.emit(renames); err != nil {
		return err
	}
	if t.comment != "" {
		if err := w.emit("COMMENT ON TABLE " + qualified + " IS " + dump.CommentLiteral(t.comment) + ";\n"); err != nil {
			return err
		}
	}
	for i, c := range t.columns {
		if text := t.columnComments[t.attributes[i]]; text != "" {
			if err := w.emit("COMMENT ON COLUMN " + qualified + "." + catalog.QuoteIdentifier(c.Name) + " IS " + dump.CommentLiteral(text) + ";\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

// dumpTableCreateSQL renders a table's CREATE TABLE (or partition child)
// statement. It writes nothing, so every rendering error precedes output.
func dumpTableCreateSQL(ctx context.Context, s *dumpScript, t *dumpRelation, attachAfterCreate bool) (string, error) {
	qualified := catalog.QuoteIdentifier(t.schema) + "." + catalog.QuoteIdentifier(t.name)
	var line strings.Builder
	if t.descriptor.Membership == nil || partitionDumpNeedsAttach(s.catalogView, t.descriptor) {
		typed := t.descriptor.OfType != (ids.ObjectAddress{}) && !attachAfterCreate
		hasElements := !typed || len(t.columns) != 0 || len(t.descriptor.Constraints.Check) != 0 || len(t.descriptor.Constraints.Unique) != 0 || len(t.descriptor.Constraints.Exclusion) != 0 || len(t.descriptor.Constraints.ForeignKeys) != 0
		line.WriteString("CREATE TABLE " + qualified)
		if typed {
			source := s.catalogView.MustType(t.descriptor.OfType)
			namespace := s.catalogView.Get(source.ObjectNamespace).Name()
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
				declaration, err := catalogColumnDeclaration(s.catalogView, c)
				if err != nil {
					return "", err
				}
				line.WriteString(" " + declaration)
				if c.Usage.Collation.ID != c.Usage.Type.Usage().Collation.ID {
					name := typedef.CollationSQLName(s.catalogView, c.Usage.Collation.ID)
					if name == "" {
						return "", fmt.Errorf("stuntgres: unsupported stored collation")
					}
					line.WriteString(" COLLATE " + name)
				}
			}
			if definition, exists := t.defaults[t.attributes[i]]; exists && provenance.ExplicitDefault && !(s.deferDefaults && t.deferredDefinitions[t.attributes[i]]) {
				text, err := defaultSQL(ctx, s.catalogView, definition.Expression)
				if err != nil {
					return "", err
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
					text, err = (scalarRenderer{owner: t.descriptor}).sql(ctx, s.catalogView, definition.Expression)
					if err != nil {
						return "", err
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
			if !c.Nullable && dumpNotNullDeclaration(s.originalCatalog, t.descriptor, t.attributes[i]) {
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
				if inheritance.Columns[constraint.Attribute].Local || !dumpNotNullDeclaration(s.originalCatalog, t.descriptor, constraint.Attribute) {
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
			if !dumpCheckDeclaration(s.originalCatalog, t.descriptor, c) {
				continue
			}
			text, err := (scalarRenderer{owner: t.descriptor, checkReplayPrecision: true}).sql(ctx, s.catalogView, c.Expression)
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
		for _, c := range t.descriptor.Constraints.Unique {
			if physicalPrimaryDeferred(t.descriptor, &c) {
				continue
			}
			index, ok := s.catalogView.Relation(c.Index)
			if !ok {
				return "", fmt.Errorf("stuntgres: missing unique dump index")
			}
			if _, moved, err := physicalCompetingIndexSource(s.originalCatalog, index); err != nil {
				return "", err
			} else if moved {
				continue
			}
			if partitionReplaySeparateConstraint(s.catalogView, index) {
				continue
			}
			text, err := (pgRenderer{view: s.catalogView}).uniqueConstraintSQL(t.descriptor, &c)
			if err != nil {
				return "", err
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
			index, ok := s.catalogView.Relation(c.Index)
			if !ok {
				return "", fmt.Errorf("stuntgres: missing exclusion dump index")
			}
			if partitionReplaySeparateConstraint(s.catalogView, index) {
				continue
			}
			text, err := indexDefinitionSQL(ctx, s.catalogView, index, indexPresentation{dump: true, exclusion: true})
			if err != nil {
				return "", err
			}
			if localElements > 0 {
				line.WriteString(", ")
			}
			localElements++
			line.WriteString("CONSTRAINT " + catalog.QuoteIdentifier(c.Name) + " " + text)
		}
		for _, f := range t.descriptor.Constraints.ForeignKeys {
			if f.NotValid || physicalSelfForeignKeyDeferred(t.descriptor, f) || inheritedForeignKeyDeferred(s.originalCatalog, t.descriptor, &f) {
				continue
			}
			text, err := (pgRenderer{view: s.catalogView}).foreignKeyConstraintSQL(t.descriptor, f)
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
		if inheritance := t.descriptor.Inheritance; inheritance != nil {
			line.WriteString(" INHERITS (")
			for i, address := range inheritance.Parents {
				if i > 0 {
					line.WriteString(", ")
				}
				parent := s.catalogView.MustRelation(address)
				namespace := s.catalogView.Get(parent.Namespace()).Name()
				line.WriteString(catalog.QuoteIdentifier(namespace) + "." + catalog.QuoteIdentifier(parent.Name()))
			}
			line.WriteString(")")
		}
	} else {
		var deferred map[int32]bool
		if s.deferDefaults {
			deferred = t.deferredDefinitions
		}
		definition, err := partitionChildSQL(ctx, s.catalogView, t.descriptor, qualified, deferred)
		if err != nil {
			return "", err
		}
		line.WriteString(definition)
	}
	key, err := partitionKeySQL(ctx, s.catalogView, t.descriptor)
	if err != nil {
		return "", err
	}
	line.WriteString(key + ";\n")
	return line.String(), nil
}
