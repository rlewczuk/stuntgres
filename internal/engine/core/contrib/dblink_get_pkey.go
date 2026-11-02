package contrib

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
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

func DblinkGetPkey[A Authority](h Host[A], n plan.InstalledDblinkGetPkeyExpr, name string) ([]sqltype.Value, error) {
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
	return dblinkPkeyValues(h.Operation().Context(), relation, n.Usage.Type)
}

// dblinkPkeyValues copies only ordered authoritative key metadata. No source
// tuples, catalog descriptor or lock contribution escape in the owned values.
func dblinkPkeyValues(ctx context.Context, relation *catalog.Relation, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if result == nil || !result.IsComposite() || !result.IsUserDefined() || result.IsDomain() || sqltype.IsPseudo(result.ID()) || result.CompositeLen() != 2 || result.CompositeMember(0).Usage.Type.ID() != sqltype.Int4 || result.CompositeMember(1).Usage.Type.ID() != sqltype.Text {
		return nil, fmt.Errorf("stuntgres: invalid dblink primary-key result")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, key := range relation.Constraints.Unique {
		if !key.Primary {
			continue
		}
		rows := make([]sqltype.Value, len(key.Attributes))
		for i, attribute := range key.Attributes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			index := relation.IndexForAttribute(attribute)
			if index < 0 || index >= len(relation.Columns) {
				return nil, fmt.Errorf("stuntgres: invalid primary-key attribute")
			}
			position, err := sqltype.Integer(result.CompositeMember(0).Usage.Type, int64(i+1))
			if err != nil {
				return nil, err
			}
			fields := []sqltype.Value{position, sqltype.String(result.CompositeMember(1).Usage.Type, relation.Columns[index].Name)}
			rows[i], err = sqltype.OwnedCompositeValue(result, fields)
			if err != nil {
				return nil, err
			}
		}
		return rows, nil
	}
	return nil, nil
}
