package control

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// dumpSection is one fixed part of the Dump script.
type dumpSection struct {
	name string // for tests and error context only
	emit func(context.Context, *dumpScript, *dumpWriter) error
}

// dumpSections is the whole emission sequence, in script order. The ordered
// events sit between the pre-data and post-data sections.
var dumpSections = [...]dumpSection{
	{"routine directive", emitDumpRoutineDirective},
	{"preamble", emitDumpPreamble},
	{"schemas", emitDumpSchemas},
	{"conversion schemas", emitDumpConversionSchemas},
	{"initial object authority", emitDumpInitialACLs},
	{"provider alterations", emitDumpProviderReplay},
	{"collation seeds", emitDumpCollationSeeds},
	{"conversion seeds", emitDumpConversionSeeds},
	{"operator family seeds", emitDumpOperatorFamilySeeds},
	{"schema comments", emitDumpSchemaComments},
	{"sequence creation", emitDumpSequenceCreation},
	{"collations and conversions", emitDumpTypeStatements},
	{"events", emitDumpEvents},
	{"index options", emitDumpIndexOptions},
	{"clustering preferences", emitDumpClusteringPreferences},
	{"replica identity", emitDumpReplicaIdentity},
	{"sequence completion", emitDumpSequenceCompletion},
	{"large objects", emitDumpLargeObjects},
	{"materialized snapshots", emitDumpMaterializedSnapshots},
	{"policies", emitDumpPolicies},
	{"row security", emitDumpRowSecurity},
	{"rules", emitDumpRules},
	{"triggers", emitDumpTriggers},
	{"foreign defaults", emitDumpForeignDefaults},
	{"attribute ranks", emitDumpAttributeRanks},
	{"inherited column storage", emitDumpDeferredColumnStorage},
	{"index storage", emitDumpIndexStorage},
	{"object ACLs", emitDumpObjectACLs},
	{"raw codec completion", emitDumpRawCodecCompletion},
	{"publications", emitDumpPublications},
	{"subscriptions", emitDumpSubscriptions},
	{"event triggers", emitDumpEventTriggers},
	{"extended statistics snapshots", emitDumpExtendedStatistics},
	{"initial privileges inventory", emitDumpInitialPrivileges},
}

func emitDumpInitialPrivileges(_ context.Context, s *dumpScript, w *dumpWriter) error {
	if s.InitialPrivileges == nil {
		return nil
	}
	comment, err := s.InitialPrivileges.Comment()
	if err != nil {
		return err
	}
	return w.emit(comment)
}

func emitDumpRoutineDirective(_ context.Context, s *dumpScript, w *dumpWriter) error {
	if len(s.OwnedRoutines) == 0 {
		return nil
	}
	return w.emit(routine.RestoreDirective + "\n")
}

func emitDumpPreamble(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpLines(w, s.Preamble)
}

func emitDumpSchemas(_ context.Context, s *dumpScript, w *dumpWriter) error {
	sort.Slice(s.Schemas, func(i, j int) bool { return s.Schemas[i].Name < s.Schemas[j].Name })
	for _, schema := range s.Schemas {
		prefix := "CREATE SCHEMA "
		if schema.Configured {
			prefix += "IF NOT EXISTS "
		}
		if err := w.emit(prefix + catalog.QuoteIdentifier(schema.Name) + " AUTHORIZATION " + catalog.QuoteIdentifier(schema.Owner) + ";\n"); err != nil {
			return err
		}
		if schema.Configured && schema.Owner != "postgres" {
			if err := w.emit("ALTER SCHEMA " + catalog.QuoteIdentifier(schema.Name) + " OWNER TO " + catalog.QuoteIdentifier(schema.Owner) + ";\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

func emitDumpConversionSchemas(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpLines(w, s.conversionSchemas)
}

func emitDumpCollationSeeds(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpLines(w, s.seedStatements)
}

func emitDumpConversionSeeds(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpLines(w, s.conversionSeeds)
}

func emitDumpOperatorFamilySeeds(_ context.Context, s *dumpScript, w *dumpWriter) error {
	if len(s.familySeeds) == 0 {
		return nil
	}
	if err := w.setOwner("postgres"); err != nil {
		return err
	}
	return emitDumpLines(w, s.familySeeds)
}

func emitDumpSchemaComments(_ context.Context, s *dumpScript, w *dumpWriter) error {
	sort.Slice(s.SchemaComments, func(i, j int) bool { return s.SchemaComments[i].Name < s.SchemaComments[j].Name })
	for _, c := range s.SchemaComments {
		literal := "NULL"
		if c.Text != "" {
			literal = dump.CommentLiteral(c.Text)
		}
		if err := w.emit("COMMENT ON SCHEMA " + catalog.QuoteIdentifier(c.Name) + " IS " + literal + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpSequenceCreation(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpLines(w, s.sequenceCreation)
}

func emitDumpTypeStatements(_ context.Context, s *dumpScript, w *dumpWriter) error {
	for _, statements := range [2][]dump.Statement{s.collationStatements, s.conversionStatements} {
		for _, declaration := range statements {
			if err := w.setOwner(declaration.Owner); err != nil {
				return err
			}
			if err := w.emit(declaration.SQL); err != nil {
				return err
			}
		}
	}
	return nil
}

// All final names and implicit backing indexes exist after the event stream.
// Replay options separately so canonical CREATE stays within its own capability.
func emitDumpIndexOptions(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	var indexes []*catalog.Relation
	for _, descriptor := range s.OriginalCatalog.Descriptors() {
		r, ok := descriptor.(*catalog.Relation)
		if !ok || r.SQLIndex == nil || len(r.StorageOptions.Base) == 0 {
			continue
		}
		ns, ok := s.OriginalCatalog.Namespace(r.Namespace())
		if !ok || ns.Temporary != (ids.SessionToken{}) {
			continue
		}
		indexes = append(indexes, r)
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int {
		if order := strings.Compare(s.OriginalCatalog.Get(a.Namespace()).Name(), s.OriginalCatalog.Get(b.Namespace()).Name()); order != 0 {
			return order
		}
		return strings.Compare(a.Name(), b.Name())
	})
	for _, r := range indexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		owner, err := s.DomainRoles.Descriptor(r.Owner)
		if err != nil {
			return err
		}
		if err := w.setOwner(owner.Name); err != nil {
			return err
		}
		qualified := catalog.QuoteIdentifier(s.OriginalCatalog.Get(r.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.Name())
		if err := w.emit("ALTER INDEX " + qualified + " SET (" + index.OptionsSQL(r.StorageOptions.Base) + ");\n"); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpSequenceCompletion(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpLines(w, s.sequenceCompletion)
}

func emitDumpMaterializedSnapshots(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	slices.SortFunc(s.Materialized, func(a, b table.DumpRelation) int {
		if order := strings.Compare(a.Schema, b.Schema); order != 0 {
			return order
		}
		return strings.Compare(a.Name, b.Name)
	})
	for i := range s.Materialized {
		if err := view.EmitMaterializedSnapshot(ctx, &s.Materialized[i], s.OriginalCatalog, w.emit); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpPolicies(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpGroups(s.CatalogView, w, s.policyGroups)
}

func emitDumpRowSecurity(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpGroups(s.CatalogView, w, s.securityGroups)
}

// emitDumpRules writes user rules post-data: importing ordinary INSERT rows
// must not rewrite fixture data before every relation and original stored
// row exists.
func emitDumpRules(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpGroups(s.CatalogView, w, s.ruleGroups)
}

// Trigger definitions follow saved data so Restore never transforms rows twice.
func emitDumpTriggers(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpGroups(s.CatalogView, w, s.triggerGroups)
}

func emitDumpForeignDefaults(_ context.Context, s *dumpScript, w *dumpWriter) error {
	if len(s.foreignDefaultStatements) == 0 {
		return nil
	}
	if err := w.forceOwner("postgres"); err != nil {
		return err
	}
	for _, statement := range s.foreignDefaultStatements {
		if err := s.emitCastConsumer(w, statement.Site, "postgres", func(emit func(string) error) error { return emit(statement.SQL) }); err != nil {
			return err
		}
	}
	// The authentic final rows remain ordinary CREATE CAST declarations,
	// ordered by their original dependency graph, exactly once after the
	// last historical foreign DEFAULT window.
	for _, event := range s.eventOrder {
		if event.Phase == dump.Declaration && s.lateForeignCasts[event.OID] {
			if err := emitDumpDeclaration(w.ctx, s, w, event); err != nil {
				return err
			}
		}
	}
	if err := w.emit("SET SESSION AUTHORIZATION DEFAULT;\n"); err != nil {
		return err
	}
	// DEFAULT is the interpreter's login identity, not necessarily postgres.
	w.currentOwner = ""
	return nil
}

// emitDumpAttributeRanks repairs array ranks. INHERITS reconstructs every
// inherited/merged declaration with rank zero. Repair only the lost metadata
// after all owner SQL, as genuine PostgreSQL catalog SQL under the
// authenticated administrator, not as replay comments.
func emitDumpAttributeRanks(_ context.Context, s *dumpScript, w *dumpWriter) error {
	rankAuthority := false
	for _, event := range s.eventOrder {
		index, exists := s.TableIndices[event.OID]
		if event.Phase != dump.Declaration || !exists {
			continue
		}
		table := s.Tables[index]
		inheritance := table.Descriptor.Inheritance
		if inheritance == nil {
			continue
		}
		for i, column := range table.Columns {
			if column.DeclaredDimensions == 0 || len(inheritance.Columns[table.Attributes[i]].Origins) == 0 {
				continue
			}
			if !rankAuthority {
				if err := w.emit("SET SESSION AUTHORIZATION DEFAULT;\n"); err != nil {
					return err
				}
				rankAuthority = true
			}
			qualified := catalog.QuoteIdentifier(s.CatalogView.Get(table.Descriptor.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.Name)
			if err := w.emit(fmt.Sprintf("UPDATE pg_catalog.pg_attribute SET attndims=%d WHERE attrelid=%s::regclass AND attname=%s;\n", column.DeclaredDimensions, dump.CommentLiteral(qualified), dump.CommentLiteral(column.Name))); err != nil {
				return err
			}
		}
	}
	return nil
}

func emitDumpLines(w *dumpWriter, lines []string) error {
	for _, line := range lines {
		if err := w.emit(line); err != nil {
			return err
		}
	}
	return nil
}

// emitDumpGroups writes table statement groups, each under its owner.
func emitDumpGroups(view catalog.Read, w *dumpWriter, groups []dump.Group) error {
	for _, group := range groups {
		if err := w.setOwner(group.Owner); err != nil {
			return err
		}
		if err := w.emitDefinition(view, dump.Statement{SQL: group.SQL, Binding: group.Binding}); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpRawCodecCompletion(_ context.Context, s *dumpScript, w *dumpWriter) error {
	if !s.rawFloat4 || !s.rawHelper {
		return nil
	}
	final, exists := s.CatalogView.Cast(sqltype.Xid, sqltype.Float4)
	if err := w.forceOwner("postgres"); err != nil {
		return err
	}
	if err := w.emit("DROP CAST (pg_catalog.xid AS pg_catalog.float4);\n"); err != nil {
		return err
	}
	if exists && final.Method != "b" {
		return w.emit(s.declarations[final.OID].SQL)
	}
	return nil
}
