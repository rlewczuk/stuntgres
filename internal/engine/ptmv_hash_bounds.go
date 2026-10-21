package engine

import (
	"stuntgres/internal/engine/catalog"
)

func validHashPartitionBound(b *catalog.PartitionMembership) bool {
	return !b.DefaultBound && b.Modulus > 0 && b.Remainder >= 0 && b.Remainder < b.Modulus && len(b.Lower) == 0 && len(b.Upper) == 0
}

func hashPartitionOverlap(a, b *catalog.PartitionMembership) bool {
	if a.Modulus > b.Modulus {
		a, b = b, a
	}
	return b.Remainder%a.Modulus == a.Remainder
}

func compareHashPartitionBound(a, b *catalog.PartitionMembership) int {
	if a.Modulus < b.Modulus || a.Modulus == b.Modulus && a.Remainder < b.Remainder {
		return -1
	}
	if a.Modulus == b.Modulus && a.Remainder == b.Remainder {
		return 0
	}
	return 1
}
