package partition

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
)

func ValidHashBound(b *catalog.PartitionMembership) bool {
	return !b.DefaultBound && b.Modulus > 0 && b.Remainder >= 0 && b.Remainder < b.Modulus && len(b.Lower) == 0 && len(b.Upper) == 0
}

func HashOverlap(a, b *catalog.PartitionMembership) bool {
	if a.Modulus > b.Modulus {
		a, b = b, a
	}
	return b.Remainder%a.Modulus == a.Remainder
}

func CompareHashBound(a, b *catalog.PartitionMembership) int {
	if a.Modulus < b.Modulus || a.Modulus == b.Modulus && a.Remainder < b.Remainder {
		return -1
	}
	if a.Modulus == b.Modulus && a.Remainder == b.Remainder {
		return 0
	}
	return 1
}

// checkHashOverlap searches adjacent sorted bounds without a modulus-sized
// residue array. Pairwise overlap arithmetic also preserves PostgreSQL's
// first occupied residue choice.
func checkHashOverlap(ctx context.Context, base catalog.Read, d *Definition) error {
	var preceding, following *catalog.Relation
	greatest := int32(0)
	for _, address := range d.Parent.Partition.Children {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := base.MustRelation(address)
		b := child.Membership
		if b.Modulus > greatest {
			greatest = b.Modulus
		}
		if CompareHashBound(b, d.Bound) <= 0 {
			if preceding == nil || CompareHashBound(preceding.Membership, b) < 0 {
				preceding = child
			}
		} else if following == nil || CompareHashBound(b, following.Membership) < 0 {
			following = child
		}
	}
	factorError := func(detail string) error {
		diagnostic := sqlError("42P17", "every hash partition modulus must be a factor of the next larger modulus").(*sqlstate.Error)
		diagnostic.Detail = detail
		return diagnostic
	}
	if preceding != nil && d.Bound.Modulus%preceding.Membership.Modulus != 0 {
		return factorError(fmt.Sprintf(`The new modulus %d is not divisible by %d, the modulus of existing partition "%s".`, d.Bound.Modulus, preceding.Membership.Modulus, preceding.Name()))
	}
	if following != nil && following.Membership.Modulus%d.Bound.Modulus != 0 {
		return factorError(fmt.Sprintf(`The new modulus %d is not a factor of %d, the modulus of existing partition "%s".`, d.Bound.Modulus, following.Membership.Modulus, following.Name()))
	}
	var conflict *catalog.Relation
	var first int32
	for _, address := range d.Parent.Partition.Children {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := base.MustRelation(address)
		if !HashOverlap(d.Bound, child.Membership) {
			continue
		}
		slot := d.Bound.Remainder
		if d.Bound.Modulus >= greatest {
			slot %= greatest
		} else if child.Membership.Modulus > d.Bound.Modulus {
			slot = child.Membership.Remainder
		}
		if conflict == nil || slot < first {
			conflict, first = child, slot
		}
	}
	if conflict != nil {
		return expr.ErrorAt("42P17", fmt.Sprintf(`partition "%s" would overlap partition "%s"`, d.Child.Name(), conflict.Name()), d.Raw.Pos)
	}
	return nil
}
