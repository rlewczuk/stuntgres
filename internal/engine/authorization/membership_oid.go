package authorization

import (
	"fmt"
	"math"
)

// AssignMembershipOIDs reserves identities for new edges in an accepted operation.
// The caller owns the shared frontier; this transform never changes its input.
func (r *Root) AssignMembershipOIDs(next uint64) (Root, uint64, error) {
	count := uint64(0)
	for _, edge := range r.edges {
		if edge.OID == 0 {
			count++
		}
	}
	if count == 0 {
		return *r, next, nil
	}
	if next == 0 || next > math.MaxUint32 || count > math.MaxUint32-next+1 {
		return *r, next, fmt.Errorf("stuntgres: membership identity exhausted")
	}
	n := *r
	n.edges = append([]Membership(nil), r.edges...)
	for i := range n.edges {
		if n.edges[i].OID == 0 {
			n.edges[i].OID = uint32(next)
			next++
		}
	}
	return n, next, nil
}
