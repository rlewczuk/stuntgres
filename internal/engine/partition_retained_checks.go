package engine

import (
	"context"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Captured with the existing replacement source mutex/frontier. Source versions
// and datum payloads remain immutable; candidate copyRows owns separate vectors.
type partitionRowHistory struct {
	relation *catalog.Relation
	store    relationStorage
	frontier ids.CommitSeq
}

func partitionNeedsRowHistory(catalog CatalogRead, d *catalog.Relation) bool {
	if d.Membership == nil || d.Partition != nil {
		return false
	}
	for _, check := range d.CheckConstraints {
		if check.NotValid {
			return partitionHashReplayTarget(catalog, d) != d
		}
	}
	return false
}

func (h partitionRowHistory) nextRow(ctx context.Context, position *int, candidate rowVersion) ([]sqltype.Value, error) {
	for *position < len(h.store.versions) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		original := h.store.versions[*position]
		*position = *position + 1
		if !liveVersion(original, h.frontier, h.store.ends, nil) {
			continue
		}
		if original.row != candidate.row || original.version != candidate.version {
			return nil, ids.ErrCatalogReplaced
		}
		return original.values, nil
	}
	return nil, ids.ErrCatalogReplaced
}

func partitionRetainedAttributeChanged(ctx context.Context, h partitionRowHistory, d *catalog.Relation, attribute int32, before, after []sqltype.Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	oldSlot, newSlot := h.relation.IndexForAttribute(attribute), d.IndexForAttribute(attribute)
	if oldSlot < 0 || newSlot < 0 {
		return false, ids.ErrCatalogReplaced
	}
	if generation, ok := h.relation.Generations[attribute]; ok && generation.Kind == catalog.GenerationVirtual {
		// Virtual cells have no stored value. Follow the already-certified
		// generation dependency graph to its actual stored input cells.
		for _, dependency := range generation.Dependencies {
			address := dependency.Address
			whole := address
			whole.SubID = 0
			if whole != h.relation.Address() {
				continue
			}
			if address.SubID == 0 {
				for _, input := range h.relation.Attributes {
					if input == attribute {
						continue
					}
					changed, err := partitionRetainedAttributeChanged(ctx, h, d, input, before, after)
					if err != nil || changed {
						return changed, err
					}
				}
			} else {
				changed, err := partitionRetainedAttributeChanged(ctx, h, d, address.SubID, before, after)
				if err != nil || changed {
					return changed, err
				}
			}
		}
		return false, ctx.Err()
	}
	same, err := before[oldSlot].SamePayload(ctx, after[newSlot])
	return !same, err
}

// Only material datum/provider changes make a retained NOT VALID check newly
// relevant. Descriptor ownership alone does not change its old-row authority.
func partitionRetainedCheckAffected(ctx context.Context, h partitionRowHistory, d *catalog.Relation, check catalog.CheckConstraint, before, after []sqltype.Value) (bool, error) {
	var original *catalog.CheckConstraint
	for i := range h.relation.CheckConstraints {
		if h.relation.CheckConstraints[i].Name == check.Name {
			original = &h.relation.CheckConstraints[i]
			break
		}
	}
	if original == nil {
		return false, ids.ErrCatalogReplaced
	}
	for _, dependency := range original.Dependencies {
		address := dependency.Ref.Address
		whole := address
		whole.SubID = 0
		if whole == h.relation.Address() {
			if address.SubID == 0 {
				for _, attribute := range h.relation.Attributes {
					changed, err := partitionRetainedAttributeChanged(ctx, h, d, attribute, before, after)
					if err != nil || changed {
						return changed, err
					}
				}
			} else {
				changed, err := partitionRetainedAttributeChanged(ctx, h, d, address.SubID, before, after)
				if err != nil || changed {
					return changed, err
				}
			}
			continue
		}
	}
	return false, ctx.Err()
}

// Trusted catalog import preserves executable provider recipes while projecting
// their ownership. Only material literal payloads or an owner-sensitive result,
// not Type/Domain/Procedure/Collation address renumbering, change that recipe's
// retained-row inputs. Compare literals before any identity normalization.
func partitionCheckRecipeAffected(ctx context.Context, before, after expr.Expr, ownerChanged bool) (bool, error) {
	if ownerChanged && generationHasTableOID(before) {
		return true, ctx.Err()
	}
	var local [16]sqltype.Value
	literals := local[:0]
	var err error
	walkBoundExpression(before, func(node expr.Expr) {
		if err != nil {
			return
		}
		err = ctx.Err()
		if err != nil {
			return
		}
		if literal, ok := node.(literalExpression); ok {
			literals = append(literals, literal.value)
		}
	})
	if err != nil {
		return false, err
	}
	position, affected := 0, false
	walkBoundExpression(after, func(node expr.Expr) {
		if err != nil {
			return
		}
		err = ctx.Err()
		if err != nil {
			return
		}
		if literal, ok := node.(literalExpression); ok {
			if position >= len(literals) {
				affected = true
			} else {
				var same bool
				same, err = literals[position].SamePayload(ctx, literal.value)
				affected = affected || !same
			}
			position++
		}
	})
	return affected || position != len(literals), err
}

// This one statement-local descriptor reuses ordinary physical validation and its
// CHECK preflight ordering. Its subset changes only when affected inputs change;
// invalidate the existing preparation entry then, never a published descriptor.
type partitionRetainedChecks struct {
	descriptor     catalog.Relation
	initialized    bool
	recipeAffected []bool
}

func (p *partitionRetainedChecks) forRow(ctx context.Context, h partitionRowHistory, d *catalog.Relation, before, after []sqltype.Value, snapshot *indexSnapshot, evaluation *EvalContext) (*catalog.Relation, error) {
	if !p.initialized {
		p.descriptor = *d
		p.descriptor.CheckConstraints = slices.Clone(d.CheckConstraints)
		p.initialized = true
		ownerChanged := snapshot.destination(h.relation.Address()) != h.relation.Address()
		for i, check := range d.CheckConstraints {
			if !check.NotValid {
				continue
			}
			var original *catalog.CheckConstraint
			for j := range h.relation.CheckConstraints {
				if h.relation.CheckConstraints[j].Name == check.Name {
					original = &h.relation.CheckConstraints[j]
					break
				}
			}
			if original == nil {
				return nil, ids.ErrCatalogReplaced
			}
			affected, err := partitionCheckRecipeAffected(ctx, original.Expression, check.Expression, ownerChanged)
			if err != nil {
				return nil, err
			}
			// Virtual generated inputs have no stored cell payload to compare.
			// Their captured recipe can still observe a reminted owner/literal.
			for _, dependency := range original.Dependencies {
				address := dependency.Ref.Address
				whole := address
				whole.SubID = 0
				if whole != h.relation.Address() {
					continue
				}
				for attribute, generation := range h.relation.Generations {
					if generation.Kind != catalog.GenerationVirtual || address.SubID != 0 && address.SubID != attribute {
						continue
					}
					current, exists := d.Generations[attribute]
					if !exists {
						return nil, ids.ErrCatalogReplaced
					}
					different, err := partitionCheckRecipeAffected(ctx, generation.Planned, current.Planned, ownerChanged)
					if err != nil {
						return nil, err
					}
					affected = affected || different
				}
			}
			if affected {
				if p.recipeAffected == nil {
					p.recipeAffected = make([]bool, len(d.CheckConstraints))
				}
				p.recipeAffected[i] = true
			}
		}
	}
	oldCount := len(p.descriptor.CheckConstraints)
	checks := p.descriptor.CheckConstraints[:cap(p.descriptor.CheckConstraints)]
	count, changed := 0, false
	for i, check := range d.CheckConstraints {
		if check.NotValid {
			affected := p.recipeAffected != nil && p.recipeAffected[i]
			if !affected {
				var err error
				affected, err = partitionRetainedCheckAffected(ctx, h, d, check, before, after)
				if err != nil {
					return nil, err
				}
			}
			if !affected {
				continue
			}
		}
		if count >= oldCount || checks[count].OID != check.OID {
			changed = true
		}
		checks[count] = check
		count++
	}
	p.descriptor.CheckConstraints = checks[:count]
	if changed || count != oldCount {
		delete(evaluation.preparedChecks, &p.descriptor)
	}
	return &p.descriptor, nil
}
