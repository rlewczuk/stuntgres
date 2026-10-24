package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoRoleRoutineGrantsOID is a native source identity, not PostgreSQL's allocated OID.
const InfoRoleRoutineGrantsOID ids.OID = 16220

// Project actual routine ACL origins whose grantor or grantee is enabled. This is
// not an effective-rights summary: PUBLIC access alone does not reveal origins,
// and owner authority changes grantability without inventing ACL entries.
func captureInfoRoleRoutineGrants(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoRoleRoutineGrantsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 10 {
		return nil, fmt.Errorf("stuntgres: invalid role_routine_grants source")
	}
	var domains, bases [10]*sqltype.Descriptor
	for i, id := range [...]sqltype.ID{13248, 13248, 13248, 13248, 13248, 13248, 13248, 13248, 13246, 13256} {
		baseID := sqltype.Name
		if i >= 8 {
			baseID = sqltype.VarChar
		}
		domain, ok := in.Catalog.LookupType(id)
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || in.Source.Columns[i].Usage.Type != domain {
			return nil, fmt.Errorf("stuntgres: invalid role_routine_grants domain")
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
		routine, ok := descriptor.(*catalog.Procedure)
		if !ok {
			continue
		}
		namespace, ok := in.Catalog.Namespace(routine.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing routine privilege namespace")
		}
		if _, err := in.Roles.Descriptor(routine.Owner); err != nil {
			return nil, err
		}
		entries := routine.ACL.Entries
		var baseline [2]authorization.ACLEntry
		if !routine.ACL.Explicit {
			baseline[0] = authorization.ACLEntry{Grantor: routine.Owner, Grantee: authorization.ACLGrantee{Role: routine.Owner}, Privileges: authorization.ExecutePrivilege}
			baseline[1] = authorization.ACLEntry{Grantor: routine.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege}
			entries = baseline[:]
		}
		var specific string
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
				const exposed = authorization.ExecutePrivilege
				if visible && entry.Privileges&exposed&^entry.GrantOptions != 0 {
					ownerGrantee = grantee.Superuser || grantee.Ref == routine.Owner
					if !ownerGrantee {
						pair := ownerPair{grantee.Ref, routine.Owner}
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
							ownerGrantee = contains(closure, routine.Owner)
							if ownership == nil {
								ownership = make(map[ownerPair]bool)
							}
							ownership[pair] = ownerGrantee
						}
					}
				}
			}
			if !visible || entry.Privileges&authorization.ExecutePrivilege == 0 {
				continue
			}
			grantable := "NO"
			if !entry.Grantee.Public && (ownerGrantee || entry.GrantOptions&authorization.ExecutePrivilege != 0) {
				grantable = "YES"
			}
			if specific == "" {
				specific = routineSpecificName(routine.Name(), routine.Address().ObjectID)
			}
			values := [10]string{grantor.Name, granteeName, database, namespace.Name(), specific, database, namespace.Name(), routine.Name(), "EXECUTE", grantable}
			row := make([]sqltype.Value, 10)
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
