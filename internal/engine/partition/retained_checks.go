package partition

import (
	"context"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func retainedAttributeChanged(ctx context.Context, h index.PartitionRowHistory, d *catalog.Relation, attribute int32, before, after []sqltype.Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	oldSlot, newSlot := h.Relation.IndexForAttribute(attribute), d.IndexForAttribute(attribute)
	if oldSlot < 0 || newSlot < 0 {
		return false, ids.ErrCatalogReplaced
	}
	if generation, ok := h.Relation.ColumnMeta.Generations[attribute]; ok && generation.Kind == catalog.GenerationVirtual {
		// Virtual cells have no stored value. Follow the already-certified
		// generation dependency graph to its actual stored input cells.
		for _, dependency := range generation.Dependencies {
			address := dependency.Address
			whole := address
			whole.SubID = 0
			if whole != h.Relation.Address() {
				continue
			}
			if address.SubID == 0 {
				for _, input := range h.Relation.Attributes {
					if input == attribute {
						continue
					}
					changed, err := retainedAttributeChanged(ctx, h, d, input, before, after)
					if err != nil || changed {
						return changed, err
					}
				}
			} else {
				changed, err := retainedAttributeChanged(ctx, h, d, address.SubID, before, after)
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
func retainedCheckAffected(ctx context.Context, h index.PartitionRowHistory, d *catalog.Relation, check catalog.CheckConstraint, before, after []sqltype.Value) (bool, error) {
	var original *catalog.CheckConstraint
	for i := range h.Relation.Constraints.Check {
		if h.Relation.Constraints.Check[i].Name == check.Name {
			original = &h.Relation.Constraints.Check[i]
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
		if whole == h.Relation.Address() {
			if address.SubID == 0 {
				for _, attribute := range h.Relation.Attributes {
					changed, err := retainedAttributeChanged(ctx, h, d, attribute, before, after)
					if err != nil || changed {
						return changed, err
					}
				}
			} else {
				changed, err := retainedAttributeChanged(ctx, h, d, address.SubID, before, after)
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
func CheckRecipeAffected(ctx context.Context, before, after expr.Expr, ownerChanged bool) (bool, error) {
	if ownerChanged && plan.GenerationHasTableOID(before) {
		return true, ctx.Err()
	}
	var local [16]sqltype.Value
	literals := local[:0]
	var err error
	plan.WalkExpression(before, func(node expr.Expr) {
		if err != nil {
			return
		}
		err = ctx.Err()
		if err != nil {
			return
		}
		if literal, ok := node.(plan.LiteralExpr); ok {
			literals = append(literals, literal.Value)
		}
	})
	if err != nil {
		return false, err
	}
	position, affected := 0, false
	plan.WalkExpression(after, func(node expr.Expr) {
		if err != nil {
			return
		}
		err = ctx.Err()
		if err != nil {
			return
		}
		if literal, ok := node.(plan.LiteralExpr); ok {
			if position >= len(literals) {
				affected = true
			} else {
				var same bool
				same, err = literals[position].SamePayload(ctx, literal.Value)
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
type RetainedChecks struct {
	descriptor     catalog.Relation
	initialized    bool
	recipeAffected []bool
}

func (p *RetainedChecks) ForRow(ctx context.Context, h index.PartitionRowHistory, d *catalog.Relation, before, after []sqltype.Value, snapshot *index.Snapshot, evaluation *exec.Context) (*catalog.Relation, error) {
	if !p.initialized {
		p.descriptor = *d
		p.descriptor.Constraints.Check = slices.Clone(d.Constraints.Check)
		p.initialized = true
		ownerChanged := snapshot.Destination(h.Relation.Address()) != h.Relation.Address()
		for i, check := range d.Constraints.Check {
			if !check.NotValid {
				continue
			}
			var original *catalog.CheckConstraint
			for j := range h.Relation.Constraints.Check {
				if h.Relation.Constraints.Check[j].Name == check.Name {
					original = &h.Relation.Constraints.Check[j]
					break
				}
			}
			if original == nil {
				return nil, ids.ErrCatalogReplaced
			}
			affected, err := CheckRecipeAffected(ctx, original.Expression, check.Expression, ownerChanged)
			if err != nil {
				return nil, err
			}
			// Virtual generated inputs have no stored cell payload to compare.
			// Their captured recipe can still observe a reminted owner/literal.
			for _, dependency := range original.Dependencies {
				address := dependency.Ref.Address
				whole := address
				whole.SubID = 0
				if whole != h.Relation.Address() {
					continue
				}
				for attribute, generation := range h.Relation.ColumnMeta.Generations {
					if generation.Kind != catalog.GenerationVirtual || address.SubID != 0 && address.SubID != attribute {
						continue
					}
					current, exists := d.ColumnMeta.Generations[attribute]
					if !exists {
						return nil, ids.ErrCatalogReplaced
					}
					different, err := CheckRecipeAffected(ctx, generation.Planned, current.Planned, ownerChanged)
					if err != nil {
						return nil, err
					}
					affected = affected || different
				}
			}
			if affected {
				if p.recipeAffected == nil {
					p.recipeAffected = make([]bool, len(d.Constraints.Check))
				}
				p.recipeAffected[i] = true
			}
		}
	}
	oldCount := len(p.descriptor.Constraints.Check)
	checks := p.descriptor.Constraints.Check[:cap(p.descriptor.Constraints.Check)]
	count, changed := 0, false
	for i, check := range d.Constraints.Check {
		if check.NotValid {
			affected := p.recipeAffected != nil && p.recipeAffected[i]
			if !affected {
				var err error
				affected, err = retainedCheckAffected(ctx, h, d, check, before, after)
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
	p.descriptor.Constraints.Check = checks[:count]
	if changed || count != oldCount {
		delete(evaluation.PreparedChecks, &p.descriptor)
	}
	return &p.descriptor, nil
}
