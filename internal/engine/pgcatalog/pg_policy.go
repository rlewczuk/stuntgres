package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// capturePGPolicy projects definitions, not the policies applicable to an actor.
func capturePGPolicy(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source, ref := in.Catalog, in.Source, in.Ref
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.PolicyClassID}
	if view.Owner() == nil || view.State() == nil || in.Owner != view.Owner() || source == nil || source.Address() != address || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || ref.Address != address || view.Get(address) != source || len(source.Columns) != 8 || in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: foreign policy source authority")
	}
	names := [...]string{"oid", "polname", "polrelid", "polcmd", "polpermissive", "polroles", "polqual", "polwithcheck"}
	for i, id := range [...]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Char, sqltype.Bool, 1028, sqltype.PgNodeTree, sqltype.PgNodeTree} {
		typ, ok := view.LookupType(id)
		if !ok || typ != source.Columns[i].Usage.Type || source.Columns[i].Name != names[i] || source.Columns[i].Nullable != (i >= 6) {
			return nil, fmt.Errorf("stuntgres: invalid policy source column")
		}
	}
	if err := in.Runtime.ValidateCapture(view, in.Roles, ref, source, ids.PolicyClassID); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	seen := make(map[ids.OID]bool)
	for _, relation := range view.Relations() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if len(relation.Security.Policies) == 0 {
			continue
		}
		if err := in.Runtime.ValidateACL(in.Roles, relation); err != nil {
			return nil, err
		}
		for _, policy := range relation.Security.Policies {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			command := ""
			switch policy.Command {
			case catalog.PolicyAll:
				command = "*"
			case catalog.PolicySelect:
				command = "r"
			case catalog.PolicyInsert:
				command = "a"
			case catalog.PolicyUpdate:
				command = "w"
			case catalog.PolicyDelete:
				command = "d"
			}
			if policy.OID == 0 || seen[policy.OID] || policy.Name == "" || command == "" || len(policy.Roles) == 0 {
				return nil, fmt.Errorf("stuntgres: malformed policy definition")
			}
			seen[policy.OID] = true
			row := make([]sqltype.Value, 8)
			var err error
			row[0], err = sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(policy.OID))
			if err != nil {
				return nil, err
			}
			row[1] = sqltype.String(source.Columns[1].Usage.Type, policy.Name)
			row[2], err = sqltype.OIDValue(source.Columns[2].Usage.Type, uint32(relation.Address().ObjectID))
			if err != nil {
				return nil, err
			}
			row[3] = sqltype.String(source.Columns[3].Usage.Type, command)
			row[4] = sqltype.Boolean(source.Columns[4].Usage.Type, policy.Permissive)
			elements := make([]sqltype.Value, len(policy.Roles))
			for i, role := range policy.Roles {
				var oid uint32
				if role.Public {
					if len(policy.Roles) != 1 {
						return nil, fmt.Errorf("stuntgres: malformed PUBLIC policy target")
					}
				} else {
					oid = uint32(role.Ref.ID())
				}
				elements[i], err = sqltype.OIDValue(source.Columns[5].Usage.Type.Element(), oid)
				if err != nil {
					return nil, err
				}
			}
			row[5], err = sqltype.OwnedArrayValue(source.Columns[5].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
			if err != nil {
				return nil, err
			}
			for i, payload := range []catalog.PolicyPayload{policy.Using, policy.Check} {
				row[6+i], err = in.Runtime.PolicyTree(in.Context, view, relation, source.Columns[6+i].Usage.Type, payload)
				if err != nil {
					return nil, err
				}
			}
			rows = append(rows, row)
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
