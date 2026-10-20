package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Reserved native source identity; not a claim about the oracle's view OID.
const tablePrivilegesOID ids.OID = 16200

// This is an ACL-origin projection, not an effective-rights summary: owner
// authority supplies grantability, never an invented origin on an explicit ACL.
// All catalog, roles and actor inputs belong to the same immutable statement.
func captureTablePrivileges(in catalogSourceInput) ([][]sqltype.Value, error) {
	ctx, view, ref, source := in.Context, in.Catalog, in.Ref, in.Source
	database := in.Runtime.DatabaseName()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(view, in.Roles, ref, source, tablePrivilegesOID); err != nil {
		return nil, err
	}
	actor, actorExists := in.Roles.Find(in.Runtime.CurrentPrincipal())
	var principals []authorization.PrincipalRef
	if actorExists && !actor.Superuser {
		var err error
		principals, err = in.Roles.RoleClosure(ctx, actor.Ref, true)
		if err != nil {
			return nil, err
		}
	}
	var granteeClosures map[authorization.PrincipalRef][]authorization.PrincipalRef
	var rows [][]sqltype.Value
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation && relation.RelationKind != catalog.ViewRelation && relation.RelationKind != catalog.CatalogRelation && relation.RelationKind != catalog.ForeignRelation {
			continue
		}
		namespace := view.Get(relation.Namespace())
		if namespace == nil {
			return nil, fmt.Errorf("stuntgres: missing privilege namespace")
		}
		owner, err := in.Roles.Descriptor(relation.Owner)
		if err != nil {
			return nil, err
		}
		entries := relation.Security.ACL.Entries
		var baseline [1]authorization.ACLEntry
		if !relation.Security.ACL.Explicit {
			baseline[0] = authorization.ACLEntry{Grantor: relation.Owner, Grantee: authorization.ACLGrantee{Role: relation.Owner}, Privileges: authorization.TableGrantPrivileges}
			entries = baseline[:]
		}
		for _, entry := range entries {
			grantor, err := in.Roles.Descriptor(entry.Grantor)
			if err != nil {
				return nil, err
			}
			granteeName := "PUBLIC"
			visible := entry.Grantee.Public || actorExists && actor.Superuser
			if !visible {
				for _, principal := range principals {
					if principal == entry.Grantor || principal == entry.Grantee.Role {
						visible = true
						break
					}
				}
			}
			ownerGrantee := false
			if !entry.Grantee.Public {
				grantee, err := in.Roles.Descriptor(entry.Grantee.Role)
				if err != nil {
					return nil, err
				}
				granteeName = grantee.Name
				if visible {
					ownerGrantee = grantee.Ref == owner.Ref || grantee.Superuser
					if !ownerGrantee {
						closure := principals
						if grantee.Ref != in.Runtime.CurrentPrincipal() {
							var known bool
							closure, known = granteeClosures[grantee.Ref]
							if !known {
								closure, err = in.Roles.RoleClosure(ctx, grantee.Ref, true)
								if err != nil {
									return nil, err
								}
								if granteeClosures == nil {
									granteeClosures = make(map[authorization.PrincipalRef][]authorization.PrincipalRef)
								}
								granteeClosures[grantee.Ref] = closure
							}
						}
						for _, principal := range closure {
							if principal == owner.Ref {
								ownerGrantee = true
								break
							}
						}
					}
				}
			}
			if !visible {
				continue
			}
			for _, privilege := range [...]struct {
				mask authorization.Privileges
				name string
			}{{authorization.InsertPrivilege, "INSERT"}, {authorization.SelectPrivilege, "SELECT"}, {authorization.UpdatePrivilege, "UPDATE"}, {authorization.DeletePrivilege, "DELETE"}, {authorization.TruncatePrivilege, "TRUNCATE"}, {authorization.ReferencesPrivilege, "REFERENCES"}, {authorization.TriggerPrivilege, "TRIGGER"}} {
				if entry.Privileges&privilege.mask == 0 {
					continue
				}
				grantable, hierarchy := "NO", "NO"
				if ownerGrantee || entry.GrantOptions&privilege.mask != 0 {
					grantable = "YES"
				}
				if privilege.mask == authorization.SelectPrivilege {
					hierarchy = "YES"
				}
				values := [8]string{grantor.Name, granteeName, database, namespace.Name(), relation.Name(), privilege.name, grantable, hierarchy}
				row := make([]sqltype.Value, len(values))
				for i, value := range values {
					row[i] = sqltype.String(source.Columns[i].Usage.Type, value)
				}
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}
