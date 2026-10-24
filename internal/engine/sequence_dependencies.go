package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// AUTO dependencies use stable attribute SubIDs, never compacted column slots.
func (s *StatementScope) stageOwnedSequences(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode, changes *session.Journal) error {
	return stageOwnedExpressionDependencies(s.op.Context(), stage, targets, mode, s, changes, nil)
}

func stageOwnedExpressionDependencies(ctx context.Context, stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode, scope *StatementScope, changes *session.Journal, project func(*catalog.Relation, *catalog.Relation) error) error {
	return stageDropProvider(ctx, stage, scope, changes, project).OwnedExpressionDependencies(targets, mode)
}

func (provider *dropProvider) OwnedExpressionDependencies(targets []ids.ObjectAddress, mode parser.DropMode) error {
	var closure []ids.ObjectAddress
	extended := false
	// Include removed hierarchy owners, not unrelated removals staged by an
	// earlier pass. Ordinary column projection retains its existing ordering.
	for address, descriptor := range provider.RemovalDescriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.TableRelation || !catalog.InheritanceParticipant(provider.base, relation) {
			continue
		}
		present := false
		for _, target := range targets {
			present = present || target == address
		}
		if !present {
			if !extended {
				targets = append([]ids.ObjectAddress(nil), targets...)
				extended = true
			}
			targets = append(targets, address)
		}
	}
	return table.OwnedSequences(provider, targets, func(ref ids.ObjectRef) error {
		if closure == nil {
			closure = make([]ids.ObjectAddress, len(targets)+1)
			copy(closure, targets)
		}
		closure[len(targets)] = ref.Address
		if err := provider.ExpressionDependencies(closure, mode, ids.ObjectAddress{}); err != nil {
			return err
		}
		if provider.scope != nil {
			return provider.Acquire(ref, false)
		}
		return nil
	})
}
