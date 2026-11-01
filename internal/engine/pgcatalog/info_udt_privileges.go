package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoUdtPrivilegesOID is a native source identity, not PostgreSQL's allocated OID.
const InfoUdtPrivilegesOID ids.OID = 16253

// captureInfoUdtPrivileges includes PUBLIC origins independently of enabled roles.
func captureInfoUdtPrivileges(in Input) ([][]sqltype.Value, error) {
	return captureUdtPrivileges(in, InfoUdtPrivilegesOID, "udt_privileges", true)
}
