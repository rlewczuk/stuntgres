package engine

import (
	"fmt"
	"slices"
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

func physicalPermutedPrimary(table *relationDescriptor) *uniqueConstraint {
	if len(table.physicalNotNull) == 0 {
		return nil
	}
	for i := range table.uniqueConstraints {
		primary := &table.uniqueConstraints[i]
		if !primary.Primary {
			continue
		}
		for _, foreign := range table.foreignKeys {
			if !foreign.NotValid && foreign.Target == table.address && foreign.Index == primary.Index && physicalSameAttributes(foreign.Referenced, primary.Attributes) && !slices.Equal(foreign.Referenced, primary.Attributes) {
				return primary
			}
		}
	}
	return nil
}

// Reuse the existing eligible-index predicate. Only real providers competing
// with a captured permuted self-FK's primary are ordered after that FK.
func physicalCompetingIndexSource(view CatalogRead, index *relationDescriptor) (*relationDescriptor, bool, error) {
	if index == nil || index.sqlIndex == nil {
		return nil, false, nil
	}
	parent, ok := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	if !ok {
		return nil, false, nil
	}
	primary := physicalPermutedPrimary(parent)
	if primary == nil || primary.Index == index.address {
		return nil, false, nil
	}
	eligible, err := foreignKeyIndexEligible(parent, index, primary.Attributes)
	if err != nil || !eligible {
		return nil, false, err
	}
	source, ok := view.state.descriptors[primary.Index].(*relationDescriptor)
	if !ok {
		return nil, false, fmt.Errorf("stuntgres: missing physical self-FK primary provider")
	}
	return source, true, nil
}

func emitPhysicalCompetingUniques(view CatalogRead, table *relationDescriptor, owner string, separate bool, emit func(string) error, setOwner func(string) error) error {
	primary := physicalPermutedPrimary(table)
	if primary == nil {
		return nil
	}
	primaryIndex := view.state.descriptors[primary.Index].(*relationDescriptor)
	if partitionReplaySeparateConstraint(view, primaryIndex) != separate {
		return nil
	}
	qualified := quoteIdentifier(view.state.descriptors[table.namespace].Name()) + "." + quoteIdentifier(table.name)
	for i := range table.uniqueConstraints {
		constraint := &table.uniqueConstraints[i]
		index := view.state.descriptors[constraint.Index].(*relationDescriptor)
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
	if provider == nil || !provider.permuted || provider.op != op || !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || definition == nil || definition.Primary || definition.Name.Text == "" || len(definition.Columns) != len(provider.primary.keys) {
		return reject()
	}
	auth, err := op.Authorization()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || auth.roleRecord != provider.authority.roleRecord || created[provider.ref.Address] != provider.ref || auth.catalog.state.descriptors[provider.ref.Address] != provider.current {
		return reject()
	}
	old := provider.current
	namespace := auth.catalog.state.descriptors[old.namespace]
	if namespace == nil || namespace.Name() != raw.Name.Parts[0].Text || old.name != raw.Name.Parts[1].Text {
		return reject()
	}
	for i, column := range definition.Columns {
		slot, exists := old.index[column.Text]
		if !exists {
			return reject()
		}
		attribute := old.attributes[slot]
		if !slices.ContainsFunc(provider.primary.keys, func(key physicalPrimaryKeyUsage) bool { return key.attribute == attribute }) {
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
