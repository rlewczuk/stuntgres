package session

import (
	"fmt"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// SettingAccess checks whether d may be set at all: startup-only and
// read-only settings are refused, and privileged ones need a parameter grant.
// allowed reports that grant; only lc_messages consults it.
func SettingAccess(d settings.Definition, allowed func(name string) (bool, error)) error {
	switch d.Access {
	case settings.StartupOnly:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be changed without restarting the server`, d.Name))
	case settings.ReadOnly:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be changed`, d.Name))
	case settings.Privileged:
		if d.Name == "lc_messages" {
			ok, err := allowed(d.Name)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		}
		return sqlError("42501", fmt.Sprintf(`permission denied to set parameter "%s"`, d.Name))
	}
	return nil
}

// SettingReadAccess applies the captured effective user's restricted-read
// authority. Requested spelling belongs in the PostgreSQL diagnostic.
func SettingReadAccess(d settings.Definition, requested string, allowed bool) error {
	if d.ReadRestricted && !allowed {
		return &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501",
			Message: fmt.Sprintf(`permission denied to examine "%s"`, requested),
			Detail:  `Only roles with privileges of the "pg_read_all_settings" role may examine this parameter.`,
		}
	}
	return nil
}
