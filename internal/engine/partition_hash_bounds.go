package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/sqlstate"
)

// Search adjacent sorted bounds without a modulus-sized residue array. Pairwise
// overlap arithmetic also preserves PostgreSQL's first occupied residue choice.
func (p *pendingPartitionDefinition) checkHashOverlap(scope *StatementScope) error {
	var preceding, following *catalog.Relation
	greatest := int32(0)
	for _, address := range p.Parent.Partition.Children {
		if err := scope.op.Context().Err(); err != nil {
			return err
		}
		child := p.stage.Base.MustRelation(address)
		b := child.Membership
		if b.Modulus > greatest {
			greatest = b.Modulus
		}
		if partition.CompareHashBound(b, p.Bound) <= 0 {
			if preceding == nil || partition.CompareHashBound(preceding.Membership, b) < 0 {
				preceding = child
			}
		} else if following == nil || partition.CompareHashBound(b, following.Membership) < 0 {
			following = child
		}
	}
	factorError := func(detail string) error {
		d := sqlError("42P17", "every hash partition modulus must be a factor of the next larger modulus").(*sqlstate.Error)
		d.Detail = detail
		return d
	}
	if preceding != nil && p.Bound.Modulus%preceding.Membership.Modulus != 0 {
		return factorError(fmt.Sprintf(`The new modulus %d is not divisible by %d, the modulus of existing partition "%s".`, p.Bound.Modulus, preceding.Membership.Modulus, preceding.Name()))
	}
	if following != nil && following.Membership.Modulus%p.Bound.Modulus != 0 {
		return factorError(fmt.Sprintf(`The new modulus %d is not a factor of %d, the modulus of existing partition "%s".`, p.Bound.Modulus, following.Membership.Modulus, following.Name()))
	}
	var conflict *catalog.Relation
	var first int32
	for _, address := range p.Parent.Partition.Children {
		if err := scope.op.Context().Err(); err != nil {
			return err
		}
		child := p.stage.Base.MustRelation(address)
		if !partition.HashOverlap(p.Bound, child.Membership) {
			continue
		}
		slot := p.Bound.Remainder
		if p.Bound.Modulus >= greatest {
			slot %= greatest
		} else if child.Membership.Modulus > p.Bound.Modulus {
			slot = child.Membership.Remainder
		}
		if conflict == nil || slot < first {
			conflict, first = child, slot
		}
	}
	if conflict != nil {
		return expr.ErrorAt("42P17", fmt.Sprintf(`partition "%s" would overlap partition "%s"`, p.Child.Name(), conflict.Name()), p.Raw.Pos)
	}
	return nil
}
