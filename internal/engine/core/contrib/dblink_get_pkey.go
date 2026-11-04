package contrib

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// ValidateDblinkGetPkey admits the current routine even for a strict NULL call.
func ValidateDblinkGetPkey[A Authority](h Host[A], n plan.InstalledDblinkGetPkeyExpr) error {
	if err := h.Active(); err != nil {
		return err
	}
	h.ReacquireOwnerEngine()
	defer h.ReleaseOwnerEngineLocked()
	auth, err := h.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	if err := dblinkRoutineAuthority(auth, n.Procedure, n.Ref); err != nil {
		return err
	}
	return stored.ValidateInstalledDblinkGetPkey(n, auth.Access().Catalog)
}

func DblinkGetPkey[A Authority](h Host[A], n plan.InstalledDblinkGetPkeyExpr, name string) ([]string, error) {
	relation, _, _, release, err := dblinkRelation(h, n.Procedure, n.Ref, name)
	if err != nil {
		return nil, err
	}
	defer release()
	// The relation wait can also cross publication of the nominal result type.
	// Never emit values using a retired result generation after waking.
	if err := ValidateDblinkGetPkey(h, n); err != nil {
		return nil, err
	}
	return dblinkPkeyNames(h.Operation().Context(), relation)
}

// Capture only genuine ordered key names. Row conversion belongs to the actual
// value-per-call owner and is not eagerly performed under the first subject.
func dblinkPkeyNames(ctx context.Context, relation *catalog.Relation) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, key := range relation.Constraints.Unique {
		if !key.Primary {
			continue
		}
		names := make([]string, len(key.Attributes))
		for i, attribute := range key.Attributes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			index := relation.IndexForAttribute(attribute)
			if index < 0 || index >= len(relation.Columns) {
				return nil, fmt.Errorf("stuntgres: invalid primary-key attribute")
			}
			names[i] = relation.Columns[index].Name
		}
		return names, nil
	}
	return nil, nil
}
