package core

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Immutable row selection is completed before any external startup callback.
// The login principal, never the subsequently selected role, owns these scopes.
func capturedRoleDefaults(record *access.RoleRecord, database ids.ObjectAddress, login authorization.PrincipalRef) map[string]session.CapturedDefault {
	if record == nil || record.Settings == nil {
		return nil
	}
	scopes := [...]access.RoleSettingKey{{}, {Database: database}, {Role: login}, {Database: database, Role: login}}
	var result map[string]session.CapturedDefault
	for i, key := range scopes {
		for _, entry := range record.Settings.Lookup(key) {
			if result == nil {
				result = make(map[string]session.CapturedDefault)
			}
			result[entry.Name] = session.CapturedDefault{Input: entry.Value, Source: session.DefaultSource(i + 1)}
		}
	}
	return result
}
