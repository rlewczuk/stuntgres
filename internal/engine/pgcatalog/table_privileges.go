package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Reserved native source identity; not a claim about the oracle's view OID.
const TablePrivilegesOID ids.OID = 16200

func captureTablePrivileges(in Input) ([][]sqltype.Value, error) {
	return captureTableGrantPrivileges(in, TablePrivilegesOID, "table_privileges", true)
}
