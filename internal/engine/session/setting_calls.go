package session

import (
	"context"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqltype"
)

// CurrentSettingLocked decodes the evaluated current_setting argument and
// returns the effective value, or a null of typ for a null name. Caller
// holds Settings.Mu; readRestricted is the captured effective-user authority.
func (s *SettingsStore) CurrentSettingLocked(ctx context.Context, registry settings.Registry, typ *sqltype.Descriptor, arg sqltype.Value, readRestricted bool) (sqltype.Value, error) {
	output := sqltype.Null(typ)
	if !arg.IsNull() {
		name, err := arg.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		d, err := s.LookupDefinition(registry, name, false)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := SettingReadAccess(d, name, readRestricted); err != nil {
			return sqltype.Value{}, err
		}
		output = sqltype.String(typ, s.Value(d.Name))
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return output, nil
}

// SetConfigInputLocked decodes the evaluated set_config arguments (name,
// value, is_local) into the definition, the setting input (nil resets) and
// the SET LOCAL flag. Caller holds Settings.Mu.
func (s *SettingsStore) SetConfigInputLocked(registry settings.Registry, args [3]sqltype.Value) (settings.Definition, *string, bool, error) {
	if args[0].IsNull() {
		return settings.Definition{}, nil, false, sqlError("22004", "SET requires parameter name")
	}
	local := false
	if !args[2].IsNull() {
		var err error
		local, err = args[2].Boolean()
		if err != nil {
			return settings.Definition{}, nil, false, err
		}
	}
	name, err := args[0].Text()
	if err != nil {
		return settings.Definition{}, nil, false, err
	}
	d, err := s.LookupDefinition(registry, name, true)
	if err != nil {
		return settings.Definition{}, nil, false, err
	}
	var input *string
	if !args[1].IsNull() {
		text, err := args[1].Text()
		if err != nil {
			return settings.Definition{}, nil, false, err
		}
		input = &text
	}
	return d, input, local, nil
}
