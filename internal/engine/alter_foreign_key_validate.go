package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/table"
)

// validateForeignKeys scans the existing rows of checks, keys of the
// candidate view, as the operation's authority over view. Only definitions an
// ALTER introduced or whose values it changed get this initial historical
// scan; the ordinary Finish checker remains responsible for later writes.
func validateForeignKeys(s *core.StatementScope, view catalog.Read, checks []table.ForeignKeyCheck) error {
	auth, err := core.OperationAuthorization(s.Op)
	if err != nil {
		return err
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	scan := table.ForeignKeyScan{View: auth.View, Env: auth, Domain: s.Engine().Catalog.Domain, Capture: s.CaptureForeignKeyRows, Input: s.Input, Output: s.Output}
	return table.ScanForeignKeys(s.Op.Context(), scan, checks)
}
