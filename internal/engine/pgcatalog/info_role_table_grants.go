package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoRoleTableGrantsOID is a native source identity, not PostgreSQL's allocated OID.
const InfoRoleTableGrantsOID ids.OID = 16218

// Project actual table ACL origins whose grantor or grantee is enabled. This is
// not an effective-rights summary: PUBLIC access alone does not reveal origins,
// and owner authority changes grantability without inventing ACL entries.
func captureInfoRoleTableGrants(in Input) ([][]sqltype.Value, error) {
	return captureTableGrantPrivileges(in, InfoRoleTableGrantsOID, "role_table_grants", false)
}

// The two views share ACL origins and grantability, but only table_privileges
// exposes PUBLIC origins independently of an enabled grantor.
func captureTableGrantPrivileges(in Input, sourceOID ids.OID, sourceName string, includePublic bool) ([][]sqltype.Value, error) {
	ctx := in.Context
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, sourceOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 8 {
		return nil, fmt.Errorf("stuntgres: invalid %s source", sourceName)
	}
	var domains, bases [8]*sqltype.Descriptor
	for i, id := range [...]sqltype.ID{13248, 13248, 13248, 13248, 13248, 13246, 13256, 13256} {
		baseID := sqltype.Name
		if i >= 5 {
			baseID = sqltype.VarChar
		}
		domain, ok := in.Catalog.LookupType(id)
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || in.Source.Columns[i].Usage.Type != domain {
			return nil, fmt.Errorf("stuntgres: invalid %s domain", sourceName)
		}
		domains[i], bases[i] = domain, base
	}
	actor, err := in.Roles.Descriptor(in.Runtime.CurrentPrincipal())
	if err != nil {
		return nil, err
	}
	var actorClosure []authorization.PrincipalRef
	if !actor.Superuser {
		actorClosure, err = in.Roles.RoleClosure(ctx, actor.Ref, true)
		if err != nil {
			return nil, err
		}
	}
	contains := func(closure []authorization.PrincipalRef, ref authorization.PrincipalRef) bool {
		for _, member := range closure {
			if member == ref {
				return true
			}
		}
		return false
	}
	var closures map[authorization.PrincipalRef][]authorization.PrincipalRef
	type ownerPair struct{ grantee, owner authorization.PrincipalRef }
	var ownership map[ownerPair]bool
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		switch relation.RelationKind {
		case catalog.TableRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.PartitionedRelation, catalog.CatalogRelation:
		default:
			continue
		}
		namespace := in.Catalog.Get(relation.Namespace())
		if namespace == nil {
			return nil, fmt.Errorf("stuntgres: missing table privilege namespace")
		}
		if _, err := in.Roles.Descriptor(relation.Owner); err != nil {
			return nil, err
		}
		entries := relation.Security.ACL.Entries
		var baseline [1]authorization.ACLEntry
		if !relation.Security.ACL.Explicit {
			baseline[0] = authorization.ACLEntry{Grantor: relation.Owner, Grantee: authorization.ACLGrantee{Role: relation.Owner}, Privileges: authorization.TableGrantPrivileges}
			entries = baseline[:]
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			grantor, err := in.Roles.Descriptor(entry.Grantor)
			if err != nil {
				return nil, err
			}
			granteeName := "PUBLIC"
			visible := actor.Superuser || contains(actorClosure, entry.Grantor) || (includePublic && entry.Grantee.Public)
			ownerGrantee := false
			if !entry.Grantee.Public {
				grantee, err := in.Roles.Descriptor(entry.Grantee.Role)
				if err != nil {
					return nil, err
				}
				granteeName = grantee.Name
				visible = visible || contains(actorClosure, grantee.Ref)
				const exposed = authorization.TableGrantPrivileges &^ authorization.MaintainPrivilege
				if visible && entry.Privileges&exposed&^entry.GrantOptions != 0 {
					ownerGrantee = grantee.Superuser || grantee.Ref == relation.Owner
					if !ownerGrantee {
						pair := ownerPair{grantee.Ref, relation.Owner}
						var known bool
						ownerGrantee, known = ownership[pair]
						if !known {
							closure := actorClosure
							if grantee.Ref != actor.Ref {
								var known bool
								closure, known = closures[grantee.Ref]
								if !known {
									closure, err = in.Roles.RoleClosure(ctx, grantee.Ref, true)
									if err != nil {
										return nil, err
									}
									if closures == nil {
										closures = make(map[authorization.PrincipalRef][]authorization.PrincipalRef)
									}
									closures[grantee.Ref] = closure
								}
							}
							ownerGrantee = contains(closure, relation.Owner)
							if ownership == nil {
								ownership = make(map[ownerPair]bool)
							}
							ownership[pair] = ownerGrantee
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
			}{
				{authorization.SelectPrivilege, "SELECT"}, {authorization.InsertPrivilege, "INSERT"}, {authorization.UpdatePrivilege, "UPDATE"}, {authorization.DeletePrivilege, "DELETE"}, {authorization.TruncatePrivilege, "TRUNCATE"}, {authorization.ReferencesPrivilege, "REFERENCES"}, {authorization.TriggerPrivilege, "TRIGGER"},
			} {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if entry.Privileges&privilege.mask == 0 {
					continue
				}
				grantable, hierarchy := "NO", "NO"
				if !entry.Grantee.Public && (ownerGrantee || entry.GrantOptions&privilege.mask != 0) {
					grantable = "YES"
				}
				if privilege.mask == authorization.SelectPrivilege {
					hierarchy = "YES"
				}
				values := [8]string{grantor.Name, granteeName, database, namespace.Name(), relation.Name(), privilege.name, grantable, hierarchy}
				row := make([]sqltype.Value, 8)
				for i, value := range values {
					row[i], err = domains[i].Wrap(sqltype.String(bases[i], value))
					if err != nil {
						return nil, err
					}
				}
				rows = append(rows, row)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
