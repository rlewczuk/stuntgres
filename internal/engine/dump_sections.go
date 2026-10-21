package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/routine"
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
	{"collation seeds", emitDumpCollationSeeds},
	{"conversion seeds", emitDumpConversionSeeds},
	{"schema comments", emitDumpSchemaComments},
	{"sequence creation", emitDumpSequenceCreation},
	{"collations and conversions", emitDumpTypeStatements},
	{"events", emitDumpEvents},
	{"sequence completion", emitDumpSequenceCompletion},
	{"materialized snapshots", emitDumpMaterializedSnapshots},
	{"policies", emitDumpPolicies},
	{"row security", emitDumpRowSecurity},
	{"rules", emitDumpRules},
	{"attribute ranks", emitDumpAttributeRanks},
}

func emitDumpRoutineDirective(_ context.Context, s *dumpScript, w *dumpWriter) error {
	if len(s.sqlRoutines) == 0 {
		return nil
	}
	return w.emit(routine.RestoreDirective + "\n")
}

func emitDumpPreamble(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpLines(w, s.preamble)
}

func emitDumpSchemas(_ context.Context, s *dumpScript, w *dumpWriter) error {
	sort.Slice(s.schemas, func(i, j int) bool { return s.schemas[i].name < s.schemas[j].name })
	for _, schema := range s.schemas {
		prefix := "CREATE SCHEMA "
		if schema.configured {
			prefix += "IF NOT EXISTS "
		}
		if err := w.emit(prefix + catalog.QuoteIdentifier(schema.name) + " AUTHORIZATION " + catalog.QuoteIdentifier(schema.owner) + ";\n"); err != nil {
			return err
		}
		if schema.configured && schema.owner != "postgres" {
			if err := w.emit("ALTER SCHEMA " + catalog.QuoteIdentifier(schema.name) + " OWNER TO " + catalog.QuoteIdentifier(schema.owner) + ";\n"); err != nil {
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

func emitDumpSchemaComments(_ context.Context, s *dumpScript, w *dumpWriter) error {
	sort.Slice(s.schemaComments, func(i, j int) bool { return s.schemaComments[i].name < s.schemaComments[j].name })
	for _, c := range s.schemaComments {
		literal := "NULL"
		if c.text != "" {
			literal = dump.CommentLiteral(c.text)
		}
		if err := w.emit("COMMENT ON SCHEMA " + catalog.QuoteIdentifier(c.name) + " IS " + literal + ";\n"); err != nil {
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

func emitDumpSequenceCompletion(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpLines(w, s.sequenceCompletion)
}

func emitDumpMaterializedSnapshots(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	slices.SortFunc(s.materialized, func(a, b dumpRelation) int {
		if order := strings.Compare(a.schema, b.schema); order != 0 {
			return order
		}
		return strings.Compare(a.name, b.name)
	})
	for i := range s.materialized {
		if err := s.materialized[i].emitMaterializedSnapshot(ctx, s.originalCatalog, w.emit); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpPolicies(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpGroups(w, s.policyGroups)
}

func emitDumpRowSecurity(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpGroups(w, s.securityGroups)
}

// emitDumpRules writes user rules post-data: importing ordinary INSERT rows
// must not rewrite fixture data before every relation and original stored
// row exists.
func emitDumpRules(_ context.Context, s *dumpScript, w *dumpWriter) error {
	return emitDumpGroups(w, s.ruleGroups)
}

// emitDumpAttributeRanks repairs array ranks. INHERITS reconstructs every
// inherited/merged declaration with rank zero. Repair only the lost metadata
// after all owner SQL, as genuine PostgreSQL catalog SQL under the
// authenticated administrator, not as replay comments.
func emitDumpAttributeRanks(_ context.Context, s *dumpScript, w *dumpWriter) error {
	rankAuthority := false
	for _, event := range s.eventOrder {
		index, exists := s.tableIndices[event.OID]
		if event.Phase != dump.Declaration || !exists {
			continue
		}
		table := s.tables[index]
		inheritance := table.descriptor.Inheritance
		if inheritance == nil {
			continue
		}
		for i, column := range table.columns {
			if column.DeclaredDimensions == 0 || len(inheritance.Columns[table.attributes[i]].Origins) == 0 {
				continue
			}
			if !rankAuthority {
				if err := w.emit("SET SESSION AUTHORIZATION DEFAULT;\n"); err != nil {
					return err
				}
				rankAuthority = true
			}
			qualified := catalog.QuoteIdentifier(s.catalogView.Get(table.descriptor.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.name)
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
func emitDumpGroups(w *dumpWriter, groups []dump.Group) error {
	for _, group := range groups {
		if err := w.setOwner(group.Owner); err != nil {
			return err
		}
		if err := w.emit(group.SQL); err != nil {
			return err
		}
	}
	return nil
}
