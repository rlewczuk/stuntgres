package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoRoleColumnGrantsOID is a native source identity, not PostgreSQL's allocated OID.
const InfoRoleColumnGrantsOID ids.OID = 16213

func captureInfoRoleColumnGrants(in Input) ([][]sqltype.Value, error) {
	return captureColumnPrivileges(in, InfoRoleColumnGrantsOID, "role_column_grants", false)
}
