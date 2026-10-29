package pgcatalog

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const PoliciesOID ids.OID = 12018

// capturePGPolicies projects every stored definition. Target-object privileges
// affect expression qualification, not which definitions are represented.
func capturePGPolicies(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source, ref := in.Catalog, in.Source, in.Ref
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: PoliciesOID}
	if view.Owner() == nil || view.State() == nil || in.Owner != view.Owner() || source == nil || source.Address() != address || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || ref.Address != address || view.Get(address) != source || len(source.Columns) != 8 || in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: foreign policies source authority")
	}
	names := [...]string{"schemaname", "tablename", "policyname", "permissive", "roles", "cmd", "qual", "with_check"}
	for i, id := range [...]sqltype.ID{sqltype.Name, sqltype.Name, sqltype.Name, sqltype.Text, 1003, sqltype.Text, sqltype.Text, sqltype.Text} {
		typ, ok := view.LookupType(id)
		if !ok || source.Columns[i].Usage != typ.Usage() || source.Columns[i].Name != names[i] || !source.Columns[i].Nullable {
			return nil, fmt.Errorf("stuntgres: invalid policies source column")
		}
	}
	if err := in.Runtime.ValidateCapture(view, in.Roles, ref, source, PoliciesOID); err != nil {
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
		if view.Get(relation.Address()) != relation {
			return nil, fmt.Errorf("stuntgres: foreign policy relation")
		}
		if err := in.Runtime.ValidateACL(in.Roles, relation); err != nil {
			return nil, err
		}
		namespace, ok := view.Namespace(relation.Namespace())
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		for _, policy := range relation.Security.Policies {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			command := ""
			switch policy.Command {
			case catalog.PolicyAll:
				command = "ALL"
			case catalog.PolicySelect:
				command = "SELECT"
			case catalog.PolicyInsert:
				command = "INSERT"
			case catalog.PolicyUpdate:
				command = "UPDATE"
			case catalog.PolicyDelete:
				command = "DELETE"
			}
			if policy.OID == 0 || seen[policy.OID] || policy.Name == "" || command == "" || len(policy.Roles) == 0 {
				return nil, fmt.Errorf("stuntgres: malformed policy definition")
			}
			seen[policy.OID] = true
			roleNames := make([]string, 0, len(policy.Roles))
			targets := make(map[authorization.PrincipalRef]bool)
			for _, target := range policy.Roles {
				if target.Public {
					if len(policy.Roles) != 1 || target.Ref != (authorization.PrincipalRef{}) {
						return nil, fmt.Errorf("stuntgres: malformed PUBLIC policy target")
					}
					roleNames = append(roleNames, "public")
					continue
				}
				role, err := in.Roles.Descriptor(target.Ref)
				if err != nil {
					return nil, err
				}
				if !targets[target.Ref] {
					targets[target.Ref] = true
					roleNames = append(roleNames, role.Name)
				}
			}
			sort.Strings(roleNames)
			roles, err := sqltype.Names(source.Columns[4].Usage.Type, roleNames)
			if err != nil {
				return nil, err
			}
			mode := "RESTRICTIVE"
			if policy.Permissive {
				mode = "PERMISSIVE"
			}
			row := []sqltype.Value{
				sqltype.String(source.Columns[0].Usage.Type, namespace.Name()),
				sqltype.String(source.Columns[1].Usage.Type, relation.Name()),
				sqltype.String(source.Columns[2].Usage.Type, policy.Name),
				sqltype.String(source.Columns[3].Usage.Type, mode), roles,
				sqltype.String(source.Columns[5].Usage.Type, command),
				sqltype.Null(source.Columns[6].Usage.Type), sqltype.Null(source.Columns[7].Usage.Type),
			}
			for i, payload := range [...]catalog.PolicyPayload{policy.Using, policy.Check} {
				if payload == nil {
					continue
				}
				display, err := in.Runtime.PolicyDisplay(in.Context, view, relation, payload)
				if err != nil {
					return nil, err
				}
				row[6+i] = sqltype.String(source.Columns[6+i].Usage.Type, display)
			}
			rows = append(rows, row)
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
