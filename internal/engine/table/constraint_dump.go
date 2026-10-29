package table

import (
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func (t *DumpRelation) HasInvalidConstraints() bool {
	for _, n := range t.NotNullConstraints {
		if n.NotValid {
			return true
		}
	}
	for _, c := range t.Descriptor.Constraints.Check {
		if c.NotValid {
			return true
		}
	}
	for _, f := range t.Descriptor.Constraints.ForeignKeys {
		if f.NotValid {
			return true
		}
	}
	return false
}

// Invalid definitions follow all ordinary rows: no validation bypass, no
// validity promotion, and the same captured owner as their table definition.
func (t *DumpRelation) EmitInvalidConstraints(ctx context.Context, catalogView catalog.Read, emit func(string) error) error {
	prefix := "ALTER TABLE " + catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name) + " ADD CONSTRAINT "
	for _, n := range t.NotNullConstraints {
		if !n.NotValid || !DumpNotNullDeclaration(catalogView, t.Descriptor, n.Attribute) {
			continue
		}
		slot := t.Descriptor.IndexForAttribute(n.Attribute)
		suffix := " NOT VALID"
		if n.NoInherit {
			suffix += " NO INHERIT"
		}
		if err := emit(prefix + catalog.QuoteIdentifier(n.Name) + " NOT NULL " + catalog.QuoteIdentifier(t.Columns[slot].Name) + suffix + ";\n"); err != nil {
			return err
		}
	}
	for _, c := range OrderedChecks(t.Descriptor) {
		if !c.NotValid || !DumpCheckDeclaration(catalogView, t.Descriptor, c) {
			continue
		}
		text, err := (stored.ScalarRenderer{Owner: t.Descriptor, CheckReplayPrecision: true}).SQL(ctx, catalogView, c.Expression)
		if err != nil {
			return err
		}
		clause := " CHECK (" + text + ")"
		if c.NoInherit {
			clause += " NO INHERIT"
		}
		property := " NOT VALID"
		if c.NotEnforced {
			property = " NOT ENFORCED"
		}
		if err := emit(prefix + catalog.QuoteIdentifier(c.Name) + clause + property + ";\n"); err != nil {
			return err
		}
	}
	for _, f := range t.Descriptor.Constraints.ForeignKeys {
		if !f.NotValid {
			continue
		}
		text, err := (stored.Deparser{View: catalogView}).ForeignKeyConstraintSQL(t.Descriptor, f)
		if err != nil {
			return err
		}
		if err := emit(prefix + catalog.QuoteIdentifier(f.Name) + " " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

func RestoreAlterConstraints(raw *parser.AlterTable) error {
	if len(raw.Actions) == 0 {
		return sqlError("0A000", "restore accepts only existing-index or NOT VALID constraint attachments")
	}
	for _, action := range raw.Actions {
		switch action.Action {
		case parser.ClusterOn:
			if len(raw.Actions) != 1 || raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || action.ClusterIndex.Text == "" {
				return sqlError("0A000", "restore accepts only unconditional qualified clustering preferences")
			}
		case parser.AddUsingIndex:
		case parser.AddUnique:
			if action.Unique == nil || len(raw.Actions) != 1 || raw.Only || raw.IfExists || !strings.HasPrefix(action.Unique.Name.Text, "__stuntgres_partition_key_") {
				return sqlError("0A000", "restore accepts only captured private virtual unique definitions")
			}
		case parser.AddExclusion:
			if action.Exclusion == nil || len(raw.Actions) != 1 || raw.IfExists || len(raw.Name.Parts) != 2 {
				return sqlError("0A000", "restore accepts only captured private exclusion definitions")
			}
			captured := strings.HasPrefix(action.Exclusion.Name.Text, "__stuntgres_partition_key_")
			scratch := strings.HasPrefix(raw.Name.Parts[1].Text, "__stuntgres_partition_replay_") && strings.HasPrefix(action.Exclusion.Name.Text, "__stuntgres_partition_replay_")
			if !captured && !scratch {
				return sqlError("0A000", "restore accepts only captured private exclusion definitions")
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
		case parser.AlterConstraintInherit:
			if !raw.Only || raw.IfExists || raw.Star || raw.Materialized || len(raw.Actions) != 1 || len(raw.Name.Parts) != 2 || action.Constraint.Text == "" || !action.InheritabilitySet || !action.NoInherit || action.EnforcementSet || action.DeferrabilitySet || action.InitiallySet {
				return sqlError("0A000", RestoreConstraintModeBoundary)
			}
		case parser.AddNotNull:
			if !action.NotValid {
				return sqlError("0A000", "restore accepts only existing-index or NOT VALID constraint attachments")
			}
			if action.NoInherit && (raw.Only || raw.IfExists || raw.Star || raw.Materialized || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 || action.Constraint.Text == "" || action.EnforcementSet || action.DeferrabilitySet || action.InitiallySet) {
				return sqlError("0A000", "restore accepts only unconditional qualified single invalid NO INHERIT not-null attachments")
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
