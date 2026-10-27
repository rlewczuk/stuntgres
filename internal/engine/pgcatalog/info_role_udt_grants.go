package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoRoleUdtGrantsOID is a native source identity, not PostgreSQL's allocated OID.
const InfoRoleUdtGrantsOID ids.OID = 16221

// Project actual type ACL origins whose grantor or grantee is enabled. This is
// not an effective-rights summary: PUBLIC access alone does not reveal origins,
// and owner authority changes grantability without inventing ACL entries.
func captureInfoRoleUdtGrants(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoRoleUdtGrantsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid role_udt_grants source")
	}
	var domains, bases [7]*sqltype.Descriptor
	for i, id := range [...]sqltype.ID{13248, 13248, 13248, 13248, 13248, 13246, 13256} {
		baseID := sqltype.Name
		if i >= 5 {
			baseID = sqltype.VarChar
		}
		domain, ok := in.Catalog.LookupType(id)
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || in.Source.Columns[i].Usage.Type != domain {
			return nil, fmt.Errorf("stuntgres: invalid role_udt_grants domain")
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
		udt, ok := descriptor.(*catalog.Type)
		if !ok {
			continue
		}
		if udt.SQLType == nil {
			return nil, fmt.Errorf("stuntgres: missing type descriptor authority")
		}
		if udt.SQLType.IsArray() || udt.SQLType.IsDomain() {
			continue
		}
		if udt.OwningRelation == (ids.ObjectAddress{}) {
			if !udt.SQLType.IsComposite() {
				continue
			}
			// The canonical pg_statistic description has a genuine composite
			// type/ACL identity, but deliberately grants no relation reader.
			// Validate its fixed self-owned authority rather than inventing a
			// backing relation or accepting arbitrary unbacked composites.
			if !udt.SQLType.Usage().CatalogNullUsage() {
				return nil, fmt.Errorf("stuntgres: missing composite relation authority")
			}
			if err := in.Runtime.ValidateType(in.Catalog, udt); err != nil {
				return nil, err
			}
		} else {
			relation, ok := in.Catalog.Relation(udt.OwningRelation)
			if !ok || relation.RowType != udt.SQLType.ID() {
				return nil, fmt.Errorf("stuntgres: foreign row type authority")
			}
		}
		namespace, ok := in.Catalog.Namespace(udt.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing type privilege namespace")
		}
		if _, err := in.Roles.Descriptor(udt.Owner); err != nil {
			return nil, err
		}
		acl, err := in.Runtime.ObjectACL(in.Catalog, udt.Address())
		if err != nil {
			return nil, err
		}
		entries := acl.Entries
		var baseline [2]authorization.ACLEntry
		if !acl.Explicit {
			baseline[0] = authorization.ACLEntry{Grantor: udt.Owner, Grantee: authorization.ACLGrantee{Role: udt.Owner}, Privileges: authorization.UsagePrivilege}
			baseline[1] = authorization.ACLEntry{Grantor: udt.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}
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
			visible := actor.Superuser || contains(actorClosure, entry.Grantor)
			ownerGrantee := false
			if !entry.Grantee.Public {
				grantee, err := in.Roles.Descriptor(entry.Grantee.Role)
				if err != nil {
					return nil, err
				}
				granteeName = grantee.Name
				visible = visible || contains(actorClosure, grantee.Ref)
				const exposed = authorization.UsagePrivilege
				if visible && entry.Privileges&exposed&^entry.GrantOptions != 0 {
					ownerGrantee = grantee.Superuser || grantee.Ref == udt.Owner
					if !ownerGrantee {
						pair := ownerPair{grantee.Ref, udt.Owner}
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
							ownerGrantee = contains(closure, udt.Owner)
							if ownership == nil {
								ownership = make(map[ownerPair]bool)
							}
							ownership[pair] = ownerGrantee
						}
					}
				}
			}
			if !visible || entry.Privileges&authorization.UsagePrivilege == 0 {
				continue
			}
			grantable := "NO"
			if !entry.Grantee.Public && (ownerGrantee || entry.GrantOptions&authorization.UsagePrivilege != 0) {
				grantable = "YES"
			}
			values := [7]string{grantor.Name, granteeName, database, namespace.Name(), udt.Name(), "TYPE USAGE", grantable}
			row := make([]sqltype.Value, 7)
			for i, value := range values {
				row[i], err = domains[i].Wrap(sqltype.String(bases[i], value))
				if err != nil {
					return nil, err
				}
			}
			rows = append(rows, row)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
