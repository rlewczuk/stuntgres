package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoRoleUsageGrantsOID is a native source identity, not PostgreSQL's allocated OID.
const InfoRoleUsageGrantsOID ids.OID = 16223

// PUBLIC access alone does not reveal origins in the role-filtered view.
func captureInfoRoleUsageGrants(in Input) ([][]sqltype.Value, error) {
	return captureUsagePrivileges(in, InfoRoleUsageGrantsOID, "role_usage_grants", false)
}
