package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
	if len(table.PhysicalNotNull) == 0 {
		return nil
	}
	for i := range table.UniqueConstraints {
		primary := &table.UniqueConstraints[i]
		if !primary.Primary {
			continue
		}
		for _, foreign := range table.ForeignKeys {
			if !foreign.NotValid && foreign.Target == table.Address() && foreign.Index == primary.Index && physicalSameAttributes(foreign.Referenced, primary.Attributes) && !slices.Equal(foreign.Referenced, primary.Attributes) {
				return primary
			}
		}
	}
	return nil
}

// Reuse the existing eligible-index predicate. Only real providers competing
// with a captured permuted self-FK's primary are ordered after that FK.
func physicalCompetingIndexSource(view catalog.Read, index *catalog.Relation) (*catalog.Relation, bool, error) {
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
	eligible, err := foreignKeyIndexEligible(parent, index, primary.Attributes)
	if err != nil || !eligible {
		return nil, false, err
	}
	source, ok := view.Relation(primary.Index)
	if !ok {
		return nil, false, fmt.Errorf("stuntgres: missing physical self-FK primary provider")
	}
	return source, true, nil
}

func emitPhysicalCompetingUniques(view catalog.Read, table *catalog.Relation, owner string, separate bool, emit func(string) error, setOwner func(string) error) error {
	primary := physicalPermutedPrimary(table)
	if primary == nil {
		return nil
	}
	primaryIndex := view.MustRelation(primary.Index)
	if partitionReplaySeparateConstraint(view, primaryIndex) != separate {
		return nil
	}
	qualified := quoteIdentifier(view.Get(table.Namespace()).Name()) + "." + quoteIdentifier(table.Name())
	for i := range table.UniqueConstraints {
		constraint := &table.UniqueConstraints[i]
		index := view.MustRelation(constraint.Index)
		if partitionReplaySeparateConstraint(view, index) {
			continue
		}
		_, deferred, err := physicalCompetingIndexSource(view, index)
		if err != nil {
			return err
		}
		if !deferred {
			continue
		}
		text, err := (pgRenderer{view: view}).uniqueConstraintSQL(table, constraint)
		if err != nil {
			return err
		}
		if err := setOwner(owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + quoteIdentifier(constraint.Name) + " " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) preparePhysicalCompetingUnique(op *Operation, raw *parser.AlterTable, provider *restoredPhysicalForeignKeyProvider, created map[ids.ObjectAddress]ids.ObjectRef) (*physicalNotNullConstructor, error) {
	if len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUnique {
		return nil, nil
	}
	definition := raw.Actions[0].Unique
	reject := func() (*physicalNotNullConstructor, error) {
		return nil, sqlError("0A000", restorePhysicalNotNullBoundary)
	}
	if provider == nil || !provider.permuted || provider.op != op || !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || definition == nil || definition.Primary || definition.Name.Text == "" || len(definition.Columns) != len(provider.primary.Keys) {
		return reject()
	}
	auth, err := op.Authorization()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || auth.roleRecord != provider.authority.roleRecord || created[provider.ref.Address] != provider.ref || auth.catalog.Get(provider.ref.Address) != provider.current {
		return reject()
	}
	old := provider.current
	namespace := auth.catalog.Get(old.Namespace())
	if namespace == nil || namespace.Name() != raw.Name.Parts[0].Text || old.Name() != raw.Name.Parts[1].Text {
		return reject()
	}
	for i, column := range definition.Columns {
		slot, exists := old.Index[column.Text]
		if !exists {
			return reject()
		}
		attribute := old.Attributes[slot]
		if !slices.ContainsFunc(provider.primary.Keys, func(key catalog.PhysicalPrimaryKeyUsage) bool { return key.Attribute == attribute }) {
			return reject()
		}
		for _, prior := range definition.Columns[:i] {
			if prior.Text == column.Text {
				return reject()
			}
		}
	}
	return &physicalNotNullConstructor{before: old, ref: provider.ref, deferred: definition, competing: true}, nil
}
