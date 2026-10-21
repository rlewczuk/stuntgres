package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) preparePhysicalCompetingUnique(op *session.Operation, raw *parser.AlterTable, provider *restoredPhysicalForeignKeyProvider, created map[ids.ObjectAddress]ids.ObjectRef) (*physicalNotNullConstructor, error) {
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
	auth, err := operationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || auth.RoleRecord != provider.authority.RoleRecord || created[provider.ref.Address] != provider.ref || auth.Catalog.Get(provider.ref.Address) != provider.current {
		return reject()
	}
	old := provider.current
	namespace := auth.Catalog.Get(old.Namespace())
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
