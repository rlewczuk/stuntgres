package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
)

func validHashPartitionBound(b *partitionMembership) bool {
	return !b.defaultBound && b.modulus > 0 && b.remainder >= 0 && b.remainder < b.modulus && len(b.lower) == 0 && len(b.upper) == 0
}
func hashPartitionOverlap(a, b *partitionMembership) bool {
	if a.modulus > b.modulus {
		a, b = b, a
	}
	return b.remainder%a.modulus == a.remainder
}
func compareHashPartitionBound(a, b *partitionMembership) int {
	if a.modulus < b.modulus || a.modulus == b.modulus && a.remainder < b.remainder {
		return -1
	}
	if a.modulus == b.modulus && a.remainder == b.remainder {
		return 0
	}
	return 1
}

// Search adjacent sorted bounds without a modulus-sized residue array. Pairwise
// overlap arithmetic also preserves PostgreSQL's first occupied residue choice.
func (p *pendingPartitionDefinition) checkHashOverlap(scope *StatementScope) error {
	var preceding, following *relationDescriptor
	greatest := int32(0)
	for _, address := range p.parent.partition.children {
		if err := scope.op.Context().Err(); err != nil {
			return err
		}
		child := p.stage.base.MustRelation(address)
		b := child.membership
		if b.modulus > greatest {
			greatest = b.modulus
		}
		if compareHashPartitionBound(b, p.bound) <= 0 {
			if preceding == nil || compareHashPartitionBound(preceding.membership, b) < 0 {
				preceding = child
			}
		} else if following == nil || compareHashPartitionBound(b, following.membership) < 0 {
			following = child
		}
	}
	factorError := func(detail string) error {
		d := sqlError("42P17", "every hash partition modulus must be a factor of the next larger modulus").(*sqlstate.Error)
		d.Detail = detail
		return d
	}
	if preceding != nil && p.bound.modulus%preceding.membership.modulus != 0 {
		return factorError(fmt.Sprintf(`The new modulus %d is not divisible by %d, the modulus of existing partition "%s".`, p.bound.modulus, preceding.membership.modulus, preceding.name))
	}
	if following != nil && following.membership.modulus%p.bound.modulus != 0 {
		return factorError(fmt.Sprintf(`The new modulus %d is not a factor of %d, the modulus of existing partition "%s".`, p.bound.modulus, following.membership.modulus, following.name))
	}
	var conflict *relationDescriptor
	var first int32
	for _, address := range p.parent.partition.children {
		if err := scope.op.Context().Err(); err != nil {
			return err
		}
		child := p.stage.base.MustRelation(address)
		if !hashPartitionOverlap(p.bound, child.membership) {
			continue
		}
		slot := p.bound.remainder
		if p.bound.modulus >= greatest {
			slot %= greatest
		} else if child.membership.modulus > p.bound.modulus {
			slot = child.membership.remainder
		}
		if conflict == nil || slot < first {
			conflict, first = child, slot
		}
	}
	if conflict != nil {
		return expr.ErrorAt("42P17", fmt.Sprintf(`partition "%s" would overlap partition "%s"`, p.child.name, conflict.name), p.rawBounds.Pos)
	}
	return nil
}
