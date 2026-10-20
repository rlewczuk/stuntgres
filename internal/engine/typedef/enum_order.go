package typedef

import (
	"stuntgres/internal/sqltype"
)

// Members are an owned mutation copy. Renumber only when an interior midpoint
// collapses in binary32; existing datum identities are never rewritten.
func EnumInsertionOrder(members []sqltype.EnumMember, at int) float32 {
	if len(members) == 0 {
		return 1
	}
	if at == 0 {
		return members[0].SortOrder - 1
	}
	if at == len(members) {
		return members[at-1].SortOrder + 1
	}
	left, right := members[at-1].SortOrder, members[at].SortOrder
	midpoint := float32((float64(left) + float64(right)) / 2)
	if midpoint == left || midpoint == right {
		for i := range members {
			members[i].SortOrder = float32(i + 1)
		}
		midpoint = float32((float64(members[at-1].SortOrder) + float64(members[at].SortOrder)) / 2)
	}
	return midpoint
}
