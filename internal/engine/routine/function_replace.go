package routine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
)

// PostgreSQL retains identity, owner and ACL while validating the replacement's
// declaration against the old one. Body/default binding remains the ordinary
// shared checked path; a failure never publishes its tentative descriptor.
func checkFunctionReplacement(h DDLHost, auth ddl.Authority, old *catalog.Procedure, result ids.OID, layout *proc.Definition) error {
	failure := func(message string) error {
		signature, err := h.RoutineSignatureSpelling(auth, old.Address().ObjectID)
		if err != nil {
			return err
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: message, Hint: "Use DROP FUNCTION " + signature + " first."}
	}
	if old.Result != result {
		return failure("cannot change return type of existing function")
	}
	if old.SQL == nil {
		return sqlError("0A000", "replacement of built-in or externally implemented functions is unsupported")
	}
	if len(old.SQL.InputFormals) != len(layout.InputFormals) {
		return fmt.Errorf("stuntgres: replacement input identity disagrees with its declaration")
	}
	for i, slot := range old.SQL.InputFormals {
		name := old.SQL.Formals[slot].Name
		if name != "" && name != layout.Formals[layout.InputFormals[i]].Name {
			return failure(fmt.Sprintf("cannot change name of input parameter %q", name))
		}
	}
	oldDefaults, newDefaults := 0, 0
	for _, formal := range old.SQL.Formals {
		if formal.Input() && formal.HasDefault {
			oldDefaults++
		}
	}
	for _, formal := range layout.Formals {
		if formal.Input() && formal.HasDefault {
			newDefaults++
		}
	}
	if newDefaults < oldDefaults {
		return failure("cannot remove parameter defaults from existing function")
	}
	return nil
}
