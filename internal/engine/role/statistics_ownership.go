package role

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/parser"
)

// PlanStatisticsOwnership resolves the recipient first. Same-owner success is
// deliberately before owner, SET ROLE and recipient schema CREATE checks.
func PlanStatisticsOwnership(h Host, auth ddl.Authority, s *parser.AlterOwner, name string, identity bool) (OwnerChange, error) {
	view := h.AccessView(auth)
	target, err := access.SchemaOwner(view, name, identity)
	if err != nil {
		return OwnerChange{}, err
	}
	old, err := ddl.LookupStatistics(h, auth, s.Name)
	if err != nil {
		return OwnerChange{}, err
	}
	if target == old.Owner {
		return OwnerChange{Address: old.Address(), Target: target}, nil
	}
	if err = view.Check(auth, access.Requirement{Object: view.Ref(old.Address()), OwnerOnly: true, OwnerNoun: "statistics object"}); err != nil {
		return OwnerChange{}, err
	}
	if err = access.OwnershipEligibility(view, auth, target, old.Namespace()); err != nil {
		return OwnerChange{}, err
	}
	return OwnerChange{Object: old, Target: target}, nil
}
