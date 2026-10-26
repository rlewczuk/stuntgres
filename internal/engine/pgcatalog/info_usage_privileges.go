package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoUsagePrivilegesOID is a native source identity, not PostgreSQL's allocated OID.
const InfoUsagePrivilegesOID ids.OID = 16233

// captureInfoUsagePrivileges includes PUBLIC origins independently of enabled roles.
func captureInfoUsagePrivileges(in Input) ([][]sqltype.Value, error) {
	return captureUsagePrivileges(in, InfoUsagePrivilegesOID, "usage_privileges", true)
}

// Project ACL origins, not effective rights. Membership controls visibility and
// named-grantee ownership controls grantability without inventing entries.
func captureUsagePrivileges(in Input, sourceOID ids.OID, sourceName string, includePublic bool) ([][]sqltype.Value, error) {
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
	for i, id := range [...]sqltype.ID{13248, 13248, 13248, 13248, 13248, 13246, 13246, 13256} {
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
		var owner authorization.PrincipalRef
		var family string
		var entries []authorization.ACLEntry
		var baseline [2]authorization.ACLEntry
		switch object := descriptor.(type) {
		case *catalog.Type:
			if object.SQLType == nil {
				return nil, fmt.Errorf("stuntgres: missing type descriptor authority")
			}
			if !object.SQLType.IsDomain() {
				continue
			}
			owner, family = object.Owner, "DOMAIN"
		case *catalog.Relation:
			if object.RelationKind != catalog.SequenceRelation {
				continue
			}
			owner, family = object.Owner, "SEQUENCE"
		case *catalog.ForeignObject:
			switch object.Address().ClassID {
			case ids.ForeignWrapperClassID:
				family = "FOREIGN DATA WRAPPER"
			case ids.ForeignServerClassID:
				family = "FOREIGN SERVER"
			default:
				continue
			}
			owner = object.Owner
		case *catalog.Collation:
			// The database catalog's encoding is UTF8 (6); -1 is universal.
			if object.Encoding != -1 && object.Encoding != nativeDatabaseEncodingID {
				continue
			}
			owner, family = object.Owner, "COLLATION"
		default:
			continue
		}
		schema := ""
		if family != "FOREIGN DATA WRAPPER" && family != "FOREIGN SERVER" {
			namespace, ok := in.Catalog.Namespace(descriptor.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing usage privilege namespace")
			}
			schema = namespace.Name()
		}
		if _, err := in.Roles.Descriptor(owner); err != nil {
			return nil, err
		}
		if family == "COLLATION" {
			baseline[0] = authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}
			entries = baseline[:1]
		} else {
			acl, err := in.Runtime.ObjectACL(in.Catalog, descriptor.Address())
			if err != nil {
				return nil, err
			}
			entries = acl.Entries
			if !acl.Explicit {
				// Released usage_privileges uses acldefault('r'), not 's',
				// for a raw-NULL sequence: that default has no USAGE origin.
				entries = nil
				if family != "SEQUENCE" {
					baseline[0] = authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.UsagePrivilege}
					entries = baseline[:1]
					if family == "DOMAIN" {
						baseline[1] = authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}
						entries = baseline[:]
					}
				}
			}
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
				const exposed = authorization.UsagePrivilege
				if visible && entry.Privileges&exposed&^entry.GrantOptions != 0 {
					ownerGrantee = grantee.Superuser || grantee.Ref == owner
					if !ownerGrantee {
						pair := ownerPair{grantee.Ref, owner}
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
							ownerGrantee = contains(closure, owner)
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
			values := [8]string{grantor.Name, granteeName, database, schema, descriptor.Name(), family, "USAGE", grantable}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
