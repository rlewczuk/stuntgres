package engine

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

// openRelationSourceLocked consumes e.mu on every return. Definition relation
// principals affect ACL checks only; the operation remains the original invoker.
func (e *Engine) openRelationSourceLocked(scope *StatementScope, ref ObjectRef, invoker PrincipalRef) (*querySource, error) {
	ctx := scope.op.Context()
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	d, exists := e.sequenceCatalogLocked(scope.owner.sequences).state.descriptors[ref.Address].(*relationDescriptor)
	if ref.owner != e.owner || ref.Epoch != e.state.epoch || !exists {
		e.mu.Unlock()
		return nil, ErrCatalogReplaced
	}
	switch d.relationKind {
	case catalogRelation:
		source, err := e.captureCatalogSourceLocked(ctx, e.sequenceCatalogLocked(scope.owner.sequences), ref, d)
		e.mu.Unlock()
		return source, err
	case sequenceRelation:
		source, err := e.captureSequenceSourceLocked(scope, ref, d)
		e.mu.Unlock()
		return source, err
	case viewRelation:
		definition := d.view
		sourceRelation, err := e.authorizeViewSourceLocked(scope, ref, definition, invoker)
		if err != nil {
			e.mu.Unlock()
			return nil, err
		}
		e.mu.Unlock()
		evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, sequenceScope: scope}
		root, err := preflightQuery(&evaluation, definition.query.Root)
		if err != nil {
			return nil, err
		}
		e.mu.Lock()
		sourceRelation, err = e.authorizeViewSourceLocked(scope, ref, definition, invoker)
		if err != nil {
			e.mu.Unlock()
			return nil, err
		}
		var source *querySource
		if definition.source == (ObjectRef{}) {
			e.mu.Unlock()
			source = &querySource{singleton: true}
		} else {
			source, err = e.openRelationSourceLocked(scope, definition.source, invoker)
			if err != nil {
				return nil, err
			}
			// Dense slots may compact, but immutable attribute identities do not.
			unchanged := len(definition.attributes) == len(sourceRelation.attributes)
			if unchanged {
				for i, attribute := range definition.attributes {
					if attribute != sourceRelation.attributes[i] {
						unchanged = false
						break
					}
				}
			}
			if !unchanged {
				layout := make([]int, len(definition.attributes))
				for i, attribute := range definition.attributes {
					layout[i] = -1
					for slot, live := range sourceRelation.attributes {
						if attribute == live {
							layout[i] = slot
							break
						}
					}
				}
				source = &querySource{ref: definition.source, input: source, layout: layout, scratch: make([]sqltype.Value, len(layout))}
			}
		}
		source.scope = scope
		rows, _, err := finalizeQuery(ctx, root, source, nil)
		if err != nil {
			return nil, err
		}
		final := rows.(*finalRows)
		bridge := &querySource{ref: ref, final: final, scope: scope}
		if final.source != nil {
			bridge.scratch = make([]sqltype.Value, len(final.schema))
		}
		return bridge, nil
	case tableRelation:
		view, err := scope.acquireDataViewLocked(e, ref)
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return captureQuerySource(ctx, view, ref)
	default:
		e.mu.Unlock()
		return nil, fmt.Errorf("stuntgres: unsupported relation source")
	}
}

// Called both before unlocked preflight and immediately before source capture:
// neither prepared descriptions nor an earlier owner-role snapshot grant access.
func (e *Engine) authorizeViewSourceLocked(scope *StatementScope, ref ObjectRef, definition *viewDefinition, invoker PrincipalRef) (*relationDescriptor, error) {
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return nil, err
	}
	auth.catalog = e.sequenceCatalogLocked(scope.owner.sequences)
	d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || ref.owner != e.owner || ref.Epoch != auth.catalog.Epoch() {
		return nil, ErrCatalogReplaced
	}
	principal := d.owner
	if definition.invoker {
		principal = invoker
	}
	auth.state.CurrentUser = principal
	if definition.source == (ObjectRef{}) {
		return nil, nil
	}
	source, ok := auth.catalog.state.descriptors[definition.source.Address].(*relationDescriptor)
	if !ok {
		return nil, ErrCatalogReplaced
	}
	indices := make([]int, 0, len(definition.reads))
	for _, attribute := range definition.reads {
		found := false
		for slot, live := range source.attributes {
			if attribute == live {
				indices = append(indices, slot)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("stuntgres: missing view source attribute")
		}
	}
	if err := auth.checkColumns(binding{ref: definition.source, relation: source}, SelectPrivilege, indices); err != nil {
		return nil, err
	}
	return source, nil
}
