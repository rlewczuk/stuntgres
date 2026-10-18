package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Reserved native source identity; not a claim about the oracle's view OID.
const tablePrivilegesOID ids.OID = 16200

func (e *Engine) initializeTablePrivileges() error {
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}
	d := e.catalogSourceDescriptor(tablePrivilegesOID, namespace, "table_privileges", 8)
	d.VirtualView = true
	for i, name := range []string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "privilege_type", "is_grantable", "with_hierarchy"} {
		id := sqltype.Name
		if i >= 5 {
			id = sqltype.VarChar
		}
		typ, ok := (e.catalog.read(e.catalog.state)).LookupType(id)
		if !ok {
			return fmt.Errorf("stuntgres: missing privilege source type")
		}
		usage := typ.Usage()
		if i >= 5 {
			usage.Collation.ID = 950
		}
		if i >= 6 {
			usage.Typmod = 7
		}
		d.Columns[i] = ColumnDescriptor{Name: name, Usage: usage, Nullable: true, NullabilityKnown: true, OriginTable: uint32(d.Address().ObjectID), OriginAttribute: int16(i + 1)}
		d.Index[name] = i
		d.Attributes[i] = int32(i + 1)
	}
	e.registerCatalogSource(d)
	return nil
}

// This is an ACL-origin projection, not an effective-rights summary: owner
// authority supplies grantability, never an invented origin on an explicit ACL.
// All catalog, roles and actor inputs belong to the same immutable statement.
func captureTablePrivileges(ctx context.Context, view CatalogRead, auth AuthorizationView, database string, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if view.Owner() == nil || view.State() == nil || auth.engine.catalog.owner() != view.Owner() || auth.catalog.State() != view.State() || auth.roleRecord == nil || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || source == nil || source.Address() != ref.Address || source.Address().ObjectID != tablePrivilegesOID || view.Get(ref.Address) != source {
		return nil, fmt.Errorf("stuntgres: invalid table_privileges capture")
	}
	if err := auth.roles.ValidatePrincipal(auth.state.CurrentUser); err != nil {
		return nil, err
	}
	actor, actorExists := auth.roles.Find(auth.state.CurrentUser)
	var principals []authorization.PrincipalRef
	if actorExists && !actor.Superuser {
		var err error
		principals, err = auth.roles.RoleClosure(ctx, actor.Ref, true)
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
		relation, ok := descriptor.(*relationDescriptor)
		if !ok || relation.RelationKind != tableRelation && relation.RelationKind != partitionedRelation && relation.RelationKind != viewRelation && relation.RelationKind != catalogRelation && relation.RelationKind != foreignRelation {
			continue
		}
		namespace := view.Get(relation.Namespace())
		if namespace == nil {
			return nil, fmt.Errorf("stuntgres: missing privilege namespace")
		}
		owner, err := auth.roles.Descriptor(relation.Owner)
		if err != nil {
			return nil, err
		}
		entries := relation.ACL.Entries
		var baseline [1]authorization.ACLEntry
		if !relation.ACL.Explicit {
			baseline[0] = authorization.ACLEntry{Grantor: relation.Owner, Grantee: authorization.ACLGrantee{Role: relation.Owner}, Privileges: authorization.TableGrantPrivileges}
			entries = baseline[:]
		}
		for _, entry := range entries {
			grantor, err := auth.roles.Descriptor(entry.Grantor)
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
				grantee, err := auth.roles.Descriptor(entry.Grantee.Role)
				if err != nil {
					return nil, err
				}
				granteeName = grantee.Name
				if visible {
					ownerGrantee = grantee.Ref == owner.Ref || grantee.Superuser
					if !ownerGrantee {
						closure := principals
						if grantee.Ref != auth.state.CurrentUser {
							var known bool
							closure, known = granteeClosures[grantee.Ref]
							if !known {
								closure, err = auth.roles.RoleClosure(ctx, grantee.Ref, true)
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
	return &querySource{ref: ref, anonymous: rows}, nil
}
