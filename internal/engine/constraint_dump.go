package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/parser"
)

func (t *dumpRelation) hasInvalidConstraints() bool {
	for _, n := range t.notNullConstraints {
		if n.NotValid {
			return true
		}
	}
	for _, c := range t.descriptor.Constraints.Check {
		if c.NotValid {
			return true
		}
	}
	for _, f := range t.descriptor.Constraints.ForeignKeys {
		if f.NotValid {
			return true
		}
	}
	return false
}

// Invalid definitions follow all ordinary rows: no validation bypass, no
// validity promotion, and the same captured owner as their table definition.
func (t *dumpRelation) emitInvalidConstraints(ctx context.Context, catalogView catalog.Read, emit func(string) error) error {
	prefix := "ALTER TABLE " + catalog.QuoteIdentifier(t.schema) + "." + catalog.QuoteIdentifier(t.name) + " ADD CONSTRAINT "
	for _, n := range t.notNullConstraints {
		if !n.NotValid || !dumpNotNullDeclaration(catalogView, t.descriptor, n.Attribute) {
			continue
		}
		slot := t.descriptor.IndexForAttribute(n.Attribute)
		if err := emit(prefix + catalog.QuoteIdentifier(n.Name) + " NOT NULL " + catalog.QuoteIdentifier(t.columns[slot].Name) + " NOT VALID;\n"); err != nil {
			return err
		}
	}
	for _, c := range orderedChecks(t.descriptor) {
		if !c.NotValid || !dumpCheckDeclaration(catalogView, t.descriptor, c) {
			continue
		}
		text, err := (scalarRenderer{owner: t.descriptor, checkReplayPrecision: true}).sql(ctx, catalogView, c.Expression)
		if err != nil {
			return err
		}
		clause := " CHECK (" + text + ")"
		if c.NoInherit {
			clause += " NO INHERIT"
		}
		if err := emit(prefix + catalog.QuoteIdentifier(c.Name) + clause + " NOT VALID;\n"); err != nil {
			return err
		}
	}
	for _, f := range t.descriptor.Constraints.ForeignKeys {
		if !f.NotValid {
			continue
		}
		text, err := (pgRenderer{view: catalogView}).foreignKeyConstraintSQL(t.descriptor, f)
		if err != nil {
			return err
		}
		if err := emit(prefix + catalog.QuoteIdentifier(f.Name) + " " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

func restoreAlterConstraints(raw *parser.AlterTable) error {
	if len(raw.Actions) == 0 {
		return sqlError("0A000", "restore accepts only existing-index or NOT VALID constraint attachments")
	}
	for _, action := range raw.Actions {
		switch action.Action {
		case parser.AddUsingIndex:
		case parser.AddUnique:
			if action.Unique == nil || len(raw.Actions) != 1 || raw.Only || raw.IfExists || !strings.HasPrefix(action.Unique.Name.Text, "__stuntgres_partition_key_") {
				return sqlError("0A000", "restore accepts only captured private virtual unique definitions")
			}
		case parser.AddExclusion:
			if action.Exclusion == nil || len(raw.Actions) != 1 || !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || !strings.HasPrefix(action.Exclusion.Name.Text, "__stuntgres_partition_key_") {
				return sqlError("0A000", "restore accepts only captured private ONLY exclusion definitions")
			}
			for _, key := range action.Exclusion.Index.Keys {
				if err := dump.RestoreScalar(key.Expression, true); err != nil {
					return err
				}
			}
			if err := dump.RestoreScalar(action.Exclusion.Index.Predicate, true); err != nil {
				return err
			}
		case parser.SetExpression:
			if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 || action.Generation == nil {
				return sqlError("0A000", "restore accepts only inherited ONLY generation repairs")
			}
			if err := dump.RestoreScalar(action.Generation.Expression, true); err != nil {
				return err
			}
		case parser.SetNotNull:
			if !raw.Only || raw.IfExists || len(raw.Actions) != 1 || len(raw.Name.Parts) != 2 {
				return sqlError("0A000", restoreNoInheritBoundary)
			}
		case parser.AddNotNull:
			if !action.NotValid {
				return sqlError("0A000", "restore accepts only existing-index or NOT VALID constraint attachments")
			}
		case parser.AddCheck:
			if action.Check == nil || !action.Check.NotValid {
				return sqlError("0A000", "restore accepts only existing-index or NOT VALID constraint attachments")
			}
			if err := dump.RestoreScalar(action.Check.Expression, true); err != nil {
				return err
			}
		case parser.AddForeignKey:
			if action.ForeignKey == nil || !action.ForeignKey.NotValid {
				return sqlError("0A000", "restore accepts only existing-index or NOT VALID constraint attachments")
			}
		default:
			return sqlError("0A000", "restore accepts only existing-index or NOT VALID constraint attachments")
		}
	}
	return nil
}
