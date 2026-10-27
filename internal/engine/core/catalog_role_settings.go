package core

import (
	"iter"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/pgcatalog"
)

// This adapter owns no registry or mutable data: its sole authority is the
// complete immutable RoleRecord paired with the statement's role capture.
type catalogRoleSettings struct{ settings *access.RoleSettings }

func settingConfig(row []access.RoleSetting) iter.Seq[string] {
	if len(row) == 0 {
		return nil
	}
	return func(yield func(string) bool) {
		for _, entry := range row {
			if !yield(entry.Name + "=" + entry.Value) {
				return
			}
		}
	}
}

func (r catalogRoleSettings) Config(role authorization.PrincipalRef) iter.Seq[string] {
	if r.settings == nil {
		return nil
	}
	return settingConfig(r.settings.Lookup(access.RoleSettingKey{Role: role}))
}

func (r catalogRoleSettings) Rows() iter.Seq2[pgcatalog.RoleSettingScope, iter.Seq[string]] {
	return func(yield func(pgcatalog.RoleSettingScope, iter.Seq[string]) bool) {
		if r.settings == nil {
			return
		}
		for key, row := range r.settings.Rows() {
			if !yield(pgcatalog.RoleSettingScope{Database: key.Database.ObjectID, Role: key.Role}, settingConfig(row)) {
				return
			}
		}
	}
}
