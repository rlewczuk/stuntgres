package table

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func physicalSameAttributes(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i, attribute := range a {
		if !slices.Contains(b, attribute) || slices.Contains(a[:i], attribute) {
			return false
		}
	}
	return true
}

func physicalPermutedPrimary(table *catalog.Relation) *catalog.UniqueConstraint {
	if len(table.Constraints.PhysicalNotNull) == 0 {
		return nil
	}
	for i := range table.Constraints.Unique {
		primary := &table.Constraints.Unique[i]
		if !primary.Primary {
			continue
		}
		for _, foreign := range table.Constraints.ForeignKeys {
			if !foreign.NotValid && foreign.Target == table.Address() && foreign.Index == primary.Index && physicalSameAttributes(foreign.Referenced, primary.Attributes) && !slices.Equal(foreign.Referenced, primary.Attributes) {
				return primary
			}
		}
	}
	return nil
}

// Reuse the existing eligible-index predicate. Only real providers competing
// with a captured permuted self-FK's primary are ordered after that FK.
func PhysicalCompetingIndexSource(view catalog.Read, index *catalog.Relation) (*catalog.Relation, bool, error) {
	if index == nil || index.SQLIndex == nil {
		return nil, false, nil
	}
	parent, ok := view.Relation(index.SQLIndex.Parent)
	if !ok {
		return nil, false, nil
	}
	primary := physicalPermutedPrimary(parent)
	if primary == nil || primary.Index == index.Address() {
		return nil, false, nil
	}
	eligible, err := ForeignKeyIndexEligible(parent, index, primary.Attributes)
	if err != nil || !eligible {
		return nil, false, err
	}
	source, ok := view.Relation(primary.Index)
	if !ok {
		return nil, false, fmt.Errorf("stuntgres: missing physical self-FK primary provider")
	}
	return source, true, nil
}

func EmitPhysicalCompetingUniques(view catalog.Read, table *catalog.Relation, owner string, separate bool, emit func(string) error, setOwner func(string) error) error {
	primary := physicalPermutedPrimary(table)
	if primary == nil {
		return nil
	}
	primaryIndex := view.MustRelation(primary.Index)
	if index.PartitionReplaySeparateConstraint(view, primaryIndex) != separate {
		return nil
	}
	qualified := catalog.QuoteIdentifier(view.Get(table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.Name())
	for i := range table.Constraints.Unique {
		constraint := &table.Constraints.Unique[i]
		idx := view.MustRelation(constraint.Index)
		if index.PartitionReplaySeparateConstraint(view, idx) {
			continue
		}
		_, deferred, err := PhysicalCompetingIndexSource(view, idx)
		if err != nil {
			return err
		}
		if !deferred {
			continue
		}
		text, err := (stored.Deparser{View: view}).UniqueConstraintSQL(table, constraint)
		if err != nil {
			return err
		}
		if err := setOwner(owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + catalog.QuoteIdentifier(constraint.Name) + " " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

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
		return false, sqlError("0A000", RestorePhysicalNotNullBoundary)
	}
	return true, nil
}

// PreparePhysicalCompetingUnique returns the deferred constructor of raw's
// competing unique key on provider's relation: raw must name that relation
// in view and list each key column once.
func PreparePhysicalCompetingUnique(view catalog.Read, raw *parser.AlterTable, provider *RestoredPhysicalForeignKeyProvider) (*PhysicalNotNullConstructor, error) {
	definition := raw.Actions[0].Unique
	reject := func() (*PhysicalNotNullConstructor, error) {
		return nil, sqlError("0A000", RestorePhysicalNotNullBoundary)
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
	return &PhysicalNotNullConstructor{Before: old, Ref: provider.Ref, Deferred: definition, Competing: true}, nil
}
