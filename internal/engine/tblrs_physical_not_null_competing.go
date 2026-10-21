package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// CompetingUniqueShape reports whether raw, issued by op, is the named,
// non-primary unique key over provider's key columns that may follow a
// permuted self-referencing foreign key; a different statement is not
// handled, a malformed one rejected.
func CompetingUniqueShape(op *session.Operation, raw *parser.AlterTable, provider *RestoredPhysicalForeignKeyProvider) (bool, error) {
	if len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUnique {
		return false, nil
	}
	definition := raw.Actions[0].Unique
	if provider == nil || !provider.Permuted || provider.Op != op || !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || definition == nil || definition.Primary || definition.Name.Text == "" || len(definition.Columns) != len(provider.Primary.Keys) {
		return false, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	return true, nil
}

// PreparePhysicalCompetingUnique returns the deferred constructor of raw's
// competing unique key on provider's relation: raw must name that relation
// in view and list each key column once.
func PreparePhysicalCompetingUnique(view catalog.Read, raw *parser.AlterTable, provider *RestoredPhysicalForeignKeyProvider) (*table.PhysicalNotNullConstructor, error) {
	definition := raw.Actions[0].Unique
	reject := func() (*table.PhysicalNotNullConstructor, error) {
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	old := provider.Current
	namespace := view.Get(old.Namespace())
	if namespace == nil || namespace.Name() != raw.Name.Parts[0].Text || old.Name() != raw.Name.Parts[1].Text {
		return reject()
	}
	for i, column := range definition.Columns {
		slot, exists := old.Index[column.Text]
		if !exists {
			return reject()
		}
		attribute := old.Attributes[slot]
		if !slices.ContainsFunc(provider.Primary.Keys, func(key catalog.PhysicalPrimaryKeyUsage) bool { return key.Attribute == attribute }) {
			return reject()
		}
		for _, prior := range definition.Columns[:i] {
			if prior.Text == column.Text {
				return reject()
			}
		}
	}
	return &table.PhysicalNotNullConstructor{Before: old, Ref: provider.Ref, Deferred: definition, Competing: true}, nil
}
