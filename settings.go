package stuntgres

import (
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/settings"
)

// SettingType selects the PostgreSQL configuration input and display rules.
type SettingType uint8

const (
	SettingBoolean SettingType = iota
	SettingString
	SettingInteger
	SettingFloating
	SettingEnum
)

// SettingAccess is fixed at registration; privileged settings are deny-only.
type SettingAccess uint8

const (
	SettingSession SettingAccess = iota
	SettingReadOnly
	SettingPrivileged
)

// SettingUnit is the underlying numeric unit, not the displayed unit.
type SettingUnit uint8

const (
	SettingUnitNone SettingUnit = iota
	SettingBytes
	SettingKilobytes
	SettingMegabytes
	SettingMilliseconds
	SettingSeconds
	SettingMinutes
)

// SettingDefinition registers typed session configuration. Bounds are inclusive;
// zero bounds mean exactly zero. Definitions must have dotted custom names.
type SettingDefinition struct {
	Name, Description      string
	Type                   SettingType
	Access                 SettingAccess
	Default                string
	Unit                   SettingUnit
	IntegerMin, IntegerMax int32
	FloatMin, FloatMax     float64
	EnumValues             []string
}

// RegisterSetting copies and validates a definition. Registration freezes when
// the first connector constructs an engine, even if no connection is opened.
func (d *Driver) RegisterSetting(def SettingDefinition) error {
	v, err := settings.Validate(settings.Definition{Name: def.Name, Description: def.Description, Type: settings.Type(def.Type), Access: settings.Access(def.Access), BootDefault: def.Default, Unit: settings.Unit(def.Unit), IntegerMin: def.IntegerMin, IntegerMax: def.IntegerMax, FloatMin: def.FloatMin, FloatMax: def.FloatMax, EnumValues: def.EnumValues})
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settingsFrozen {
		return engine.RegistrationError("setting registry is frozen")
	}
	for _, old := range d.settings {
		if old.Name == v.Name {
			return engine.RegistrationError("duplicate setting " + v.Name)
		}
	}
	d.settings = append(d.settings, v)
	return nil
}
