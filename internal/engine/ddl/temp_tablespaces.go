package ddl

import (
	"errors"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// ValidateTempTablespaces is the effect-free declaration check. It shares
// parsing/resolution with preparation without allocating an unused OID list.
func ValidateTempTablespaces(view access.View, env access.Environment, value string) error {
	names, err := settings.ParseIdentifierList("temp_tablespaces", value)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, err := resolveTempTablespace(view, env, name); err != nil {
			return err
		}
	}
	return nil
}

func resolveTempTablespace(view access.View, env access.Environment, name string) (ids.OID, error) {
	if name == "" || name == "pg_default" {
		return 0, nil
	}
	ref, err := TablespaceRef(view, name)
	if err != nil {
		return 0, err
	}
	if err := CheckAlterTablespaceCreate(view, env, ref); err != nil {
		return 0, err
	}
	return ref.Address.ObjectID, nil
}

// PrepareTempTablespaces resolves the real provider's destinations once. SET
// rejects missing/denied members; a retained value silently omits those members
// at first use in a later transaction. Zero denotes the database default.
func PrepareTempTablespaces(view access.View, env access.Environment, value string, interactive bool) ([]ids.OID, error) {
	names, err := settings.ParseIdentifierList("temp_tablespaces", value)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	var spaces []ids.OID
	for _, name := range names {
		space, err := resolveTempTablespace(view, env, name)
		if err != nil {
			var diagnostic *sqlstate.Error
			if !interactive && errors.As(err, &diagnostic) && (diagnostic.Code == "42704" || diagnostic.Code == "42501") {
				continue
			}
			return nil, err
		}
		if spaces == nil {
			spaces = make([]ids.OID, 0, len(names))
		}
		spaces = append(spaces, space)
	}
	return spaces, nil
}

// CheckTempTablespaceCreation checks the selected destination, not the whole
// list. Consumption precedes this check, so failures do not rewind selection.
func CheckTempTablespaceCreation(view access.View, env access.Environment, destination ids.OID) error {
	if destination == 0 || destination == 1663 {
		return nil
	}
	address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: destination}
	if err := view.CheckCreate(env, view.Ref(address)); err != nil {
		return err
	}
	if destination == 1664 {
		return sqlError("22023", "only shared relations can be placed in pg_global tablespace")
	}
	return nil
}
