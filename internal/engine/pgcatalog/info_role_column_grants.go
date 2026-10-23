package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoRoleColumnGrantsOID is a native source identity, not PostgreSQL's allocated OID.
const InfoRoleColumnGrantsOID ids.OID = 16213

// Raw grantability belongs in the UNION key: ownership can subsequently turn
// distinct false/true origins into two identical YES rows.
type columnGrantOrigin struct {
	grantor   authorization.PrincipalRef
	grantee   authorization.ACLGrantee
	privilege authorization.Privileges
	option    bool
}

func captureInfoRoleColumnGrants(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoRoleColumnGrantsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 8 {
		return nil, fmt.Errorf("stuntgres: invalid role_column_grants source")
	}
	var domains, bases [8]*sqltype.Descriptor
	for i, id := range [...]sqltype.ID{13248, 13248, 13248, 13248, 13248, 13248, 13246, 13256} {
		baseID := sqltype.Name
		if i >= 6 {
			baseID = sqltype.VarChar
		}
		domain, ok := in.Catalog.LookupType(id)
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || in.Source.Columns[i].Usage.Type != domain {
			return nil, fmt.Errorf("stuntgres: invalid role_column_grants domain")
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
	// Closures depend only on the captured role/catalog frontier, not attributes.
	var closures map[authorization.PrincipalRef][]authorization.PrincipalRef
	type ownerPair struct{ grantee, owner authorization.PrincipalRef }
	var ownership map[ownerPair]bool
	rows := make([][]sqltype.Value, 0)
	seen := make(map[columnGrantOrigin]struct{})
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
			return nil, fmt.Errorf("stuntgres: missing column privilege namespace")
		}
		if _, err := in.Roles.Descriptor(relation.Owner); err != nil {
			return nil, err
		}
		entries := relation.Security.ACL.Entries
		var baseline [1]authorization.ACLEntry
		if !relation.Security.ACL.Explicit {
			baseline[0] = authorization.ACLEntry{Grantor: relation.Owner, Grantee: authorization.ACLGrantee{Role: relation.Owner}, Privileges: authorization.ColumnGrantPrivileges}
			entries = baseline[:]
		}
		for index, attribute := range relation.Attributes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if attribute <= 0 {
				continue
			}
			if index >= len(relation.Columns) {
				return nil, fmt.Errorf("stuntgres: invalid column privilege attribute")
			}
			clear(seen)
			// Table expansion and the real column ACL share a raw-origin UNION.
			for _, origins := range [2][]authorization.ACLEntry{entries, relation.Security.ColumnACL[attribute].Entries} {
				for _, entry := range origins {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if entry.Privileges&authorization.ColumnGrantPrivileges == 0 {
						continue
					}
					grantor, err := in.Roles.Descriptor(entry.Grantor)
					if err != nil {
						return nil, err
					}
					granteeName := "PUBLIC"
					visible := actor.Superuser || contains(actorClosure, entry.Grantor)
					ownerGrantee := false
					if !entry.Grantee.Public {
						grantee, err := in.Roles.Descriptor(entry.Grantee.Role)
						if err != nil {
							return nil, err
						}
						granteeName = grantee.Name
						visible = visible || contains(actorClosure, grantee.Ref)
						if visible {
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
						{authorization.SelectPrivilege, "SELECT"}, {authorization.InsertPrivilege, "INSERT"}, {authorization.UpdatePrivilege, "UPDATE"}, {authorization.ReferencesPrivilege, "REFERENCES"},
					} {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
						if entry.Privileges&privilege.mask == 0 {
							continue
						}
						option := entry.GrantOptions&privilege.mask != 0
						key := columnGrantOrigin{entry.Grantor, entry.Grantee, privilege.mask, option}
						if _, duplicate := seen[key]; duplicate {
							continue
						}
						seen[key] = struct{}{}
						grantable := "NO"
						if !entry.Grantee.Public && (option || ownerGrantee) {
							grantable = "YES"
						}
						values := [8]string{grantor.Name, granteeName, database, namespace.Name(), relation.Name(), relation.Columns[index].Name, privilege.name, grantable}
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
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
