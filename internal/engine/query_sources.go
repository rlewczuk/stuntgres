package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
)

// Each manifest entry is an SQL occurrence, even when physical identity repeats.
type queryLeaf struct {
	ref      ObjectRef
	relation *relationDescriptor
	storage  *relationStorage
	uses     []int
}

func (s *StatementScope) acquireQueryRelations(sel *parser.Select, execute bool) error {
	for _, table := range parser.FromTables(sel.From) {
		if err := s.acquireRelationTarget(&parser.Select{From: table}, execute, true); err != nil {
			return err
		}
	}
	return nil
}

// Validate exact admission identity without resolving SQL names again. Metadata
// revisions may refresh authority before binding, never an already bound layout.
func currentQuerySourceLocked(e *Engine, catalog CatalogRead, source queryLeaf) (*relationDescriptor, error) {
	current, ok := catalog.state.descriptors[source.ref.Address].(*relationDescriptor)
	if !ok || source.relation == nil || source.ref.owner != e.owner || source.ref.Epoch != e.state.epoch || source.ref.Address.SubID != 0 ||
		current.address != source.relation.address || current.namespace != source.relation.namespace || current.name != source.relation.name || current.relationKind != source.relation.relationKind ||
		!sameRelationRowShape(current, source.relation) {
		return nil, ErrCatalogReplaced
	}
	if current.relationKind == sequenceRelation {
		if current.sequence == nil || source.storage != nil || current.sequencePlanStamp != source.relation.sequencePlanStamp {
			return nil, ErrCatalogReplaced
		}
	} else if e.storage[source.ref.Address] != source.storage {
		return nil, ErrCatalogReplaced
	}
	return current, nil
}

func (s *StatementScope) validateQuerySources(leaves []queryLeaf) error {
	if len(leaves) != len(s.querySources) {
		return ErrCatalogReplaced
	}
	for i, leaf := range leaves {
		grant := s.querySources[i]
		if leaf.ref != grant.ref || leaf.relation != grant.relation || leaf.storage != grant.storage {
			return ErrCatalogReplaced
		}
	}
	return nil
}

// Called once with e.mu held: all table roots share this statement frontier.
func (e *Engine) captureQuerySourcesLocked(scope *StatementScope, leaves []queryLeaf, singleton bool) (*querySource, error) {
	if len(leaves) == 0 {
		return &querySource{singleton: singleton, scope: scope}, nil
	}
	var single [1]ObjectRef
	refs := single[:0]
	if len(leaves) > 1 {
		refs = make([]ObjectRef, 0, len(leaves))
	}
	for _, leaf := range leaves {
		if leaf.relation.relationKind != tableRelation {
			continue
		}
		found := false
		for _, ref := range refs {
			if ref == leaf.ref {
				found = true
				break
			}
		}
		if !found {
			refs = append(refs, leaf.ref)
		}
	}
	var view *VisibilityView
	var err error
	if len(refs) != 0 {
		view, err = scope.acquireDataViewsLocked(e, refs)
		if err != nil {
			return nil, err
		}
	}
	var first *querySource
	var sources []*querySource
	if len(leaves) > 1 {
		sources = make([]*querySource, len(leaves))
	}
	for i, leaf := range leaves {
		var source *querySource
		if leaf.relation.relationKind == sequenceRelation {
			source, err = e.captureSequenceSourceLocked(scope, leaf.ref, leaf.relation)
		} else {
			source, err = captureQueryPrefix(view, leaf.ref)
		}
		if err != nil {
			if first != nil {
				first.close()
			}
			for _, prior := range sources {
				if prior != nil {
					prior.close()
				}
			}
			return nil, err
		}
		source.scope = scope
		if i == 0 {
			first = source
		}
		if sources != nil {
			sources[i] = source
		}
	}
	if first == nil {
		return nil, fmt.Errorf("stuntgres: missing query source manifest")
	}
	first.sources = sources
	return first, nil
}

func selectQuerySources(ctx context.Context, source *querySource) error {
	selectOne := func(source *querySource) error {
		if source.view == nil {
			return ctx.Err()
		}
		data := source.view.read()
		_, err := selectQuerySource(ctx, source, data.frontier, data.ends[source.ref.Address], data.deleted[source.ref.Address])
		return err
	}
	if source.sources == nil {
		if err := selectOne(source); err != nil {
			source.close()
			return err
		}
		return nil
	}
	for _, leaf := range source.sources {
		if err := selectOne(leaf); err != nil {
			source.close()
			return err
		}
	}
	return nil
}
