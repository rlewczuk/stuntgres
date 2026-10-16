package engine

import (
	"context"
	"strings"
	"stuntgres/internal/parser"
)

func (t *dumpRelation) hasInvalidConstraints() bool {
	for _, n := range t.notNullConstraints {
		if n.NotValid {
			return true
		}
	}
	for _, c := range t.descriptor.checkConstraints {
		if c.NotValid {
			return true
		}
	}
	for _, f := range t.descriptor.foreignKeys {
		if f.NotValid {
			return true
		}
	}
	return false
}

// Invalid definitions follow all ordinary rows: no validation bypass, no
// validity promotion, and the same captured owner as their table definition.
func (t *dumpRelation) emitInvalidConstraints(ctx context.Context, catalog CatalogRead, emit func(string) error) error {
	prefix := "ALTER TABLE " + quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name) + " ADD CONSTRAINT "
	for _, n := range t.notNullConstraints {
		if !n.NotValid || !dumpNotNullDeclaration(catalog, t.descriptor, n.Attribute) {
			continue
		}
		slot := t.descriptor.indexForAttribute(n.Attribute)
		if err := emit(prefix + quoteIdentifier(n.Name) + " NOT NULL " + quoteIdentifier(t.columns[slot].Name) + " NOT VALID;\n"); err != nil {
			return err
		}
	}
	for _, c := range orderedChecks(t.descriptor) {
		if !c.NotValid || !dumpCheckDeclaration(catalog, t.descriptor, c) {
			continue
		}
		text, err := (scalarRenderer{owner: t.descriptor, checkReplayPrecision: true}).sql(ctx, catalog, c.expression)
		if err != nil {
			return err
		}
		clause := " CHECK (" + text + ")"
		if c.NoInherit {
			clause += " NO INHERIT"
		}
		if err := emit(prefix + quoteIdentifier(c.Name) + clause + " NOT VALID;\n"); err != nil {
			return err
		}
	}
	for _, f := range t.descriptor.foreignKeys {
		if !f.NotValid {
			continue
		}
		text, err := (pgRenderer{view: catalog}).foreignKeyConstraintSQL(t.descriptor, f)
		if err != nil {
			return err
		}
		if err := emit(prefix + quoteIdentifier(f.Name) + " " + text + ";\n"); err != nil {
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
				if err := restoreScalar(key.Expression, true); err != nil {
					return err
				}
			}
			if err := restoreScalar(action.Exclusion.Index.Predicate, true); err != nil {
				return err
			}
		case parser.SetExpression:
			if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 || action.Generation == nil {
				return sqlError("0A000", "restore accepts only inherited ONLY generation repairs")
			}
			if err := restoreScalar(action.Generation.Expression, true); err != nil {
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
			if err := restoreScalar(action.Check.Expression, true); err != nil {
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
