package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoRoleRoutineGrantsOID is a native source identity, not PostgreSQL's allocated OID.
const InfoRoleRoutineGrantsOID ids.OID = 16220

func captureInfoRoleRoutineGrants(in Input) ([][]sqltype.Value, error) {
	return captureRoutinePrivileges(in, InfoRoleRoutineGrantsOID, "role_routine_grants", false)
}
