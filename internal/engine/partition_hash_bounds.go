package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
)

func validHashPartitionBound(b *partitionMembership) bool {
	return !b.DefaultBound && b.Modulus > 0 && b.Remainder >= 0 && b.Remainder < b.Modulus && len(b.Lower) == 0 && len(b.Upper) == 0
}
func hashPartitionOverlap(a, b *partitionMembership) bool {
	if a.Modulus > b.Modulus {
		a, b = b, a
	}
	return b.Remainder%a.Modulus == a.Remainder
}
func compareHashPartitionBound(a, b *partitionMembership) int {
	if a.Modulus < b.Modulus || a.Modulus == b.Modulus && a.Remainder < b.Remainder {
		return -1
	}
	if a.Modulus == b.Modulus && a.Remainder == b.Remainder {
		return 0
	}
	return 1
}

// Search adjacent sorted bounds without a modulus-sized residue array. Pairwise
// overlap arithmetic also preserves PostgreSQL's first occupied residue choice.
func (p *pendingPartitionDefinition) checkHashOverlap(scope *StatementScope) error {
	var preceding, following *relationDescriptor
	greatest := int32(0)
	for _, address := range p.parent.Partition.Children {
		if err := scope.op.Context().Err(); err != nil {
			return err
		}
		child := p.stage.base.MustRelation(address)
		b := child.Membership
		if b.Modulus > greatest {
			greatest = b.Modulus
		}
		if compareHashPartitionBound(b, p.bound) <= 0 {
			if preceding == nil || compareHashPartitionBound(preceding.Membership, b) < 0 {
				preceding = child
			}
		} else if following == nil || compareHashPartitionBound(b, following.Membership) < 0 {
			following = child
		}
	}
	factorError := func(detail string) error {
		d := sqlError("42P17", "every hash partition modulus must be a factor of the next larger modulus").(*sqlstate.Error)
		d.Detail = detail
		return d
	}
	if preceding != nil && p.bound.Modulus%preceding.Membership.Modulus != 0 {
		return factorError(fmt.Sprintf(`The new modulus %d is not divisible by %d, the modulus of existing partition "%s".`, p.bound.Modulus, preceding.Membership.Modulus, preceding.name))
	}
	if following != nil && following.Membership.Modulus%p.bound.Modulus != 0 {
		return factorError(fmt.Sprintf(`The new modulus %d is not a factor of %d, the modulus of existing partition "%s".`, p.bound.Modulus, following.Membership.Modulus, following.name))
	}
	var conflict *relationDescriptor
	var first int32
	for _, address := range p.parent.Partition.Children {
		if err := scope.op.Context().Err(); err != nil {
			return err
		}
		child := p.stage.base.MustRelation(address)
		if !hashPartitionOverlap(p.bound, child.Membership) {
			continue
		}
		slot := p.bound.Remainder
		if p.bound.Modulus >= greatest {
			slot %= greatest
		} else if child.Membership.Modulus > p.bound.Modulus {
			slot = child.Membership.Remainder
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
