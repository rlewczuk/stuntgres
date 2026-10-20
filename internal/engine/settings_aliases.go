package engine

import "stuntgres/internal/engine/settings"

// Temporary aliases for the settings split (8b); 8c removes them. The root
// driver and cmd/stuntgres name the exported ones until they import settings.
type (
	SettingType            = settings.Type
	SettingAccess          = settings.Access
	SettingUnit            = settings.Unit
	SettingDefinition      = settings.Definition
	SessionDefaultProvider = settings.SessionDefaultProvider
	settingValue           = settings.Value
	settingsRegistry       = settings.Registry
)

const (
	SettingBoolean  = settings.Boolean
	SettingString   = settings.String
	SettingInteger  = settings.Integer
	SettingFloating = settings.Floating
	SettingEnum     = settings.Enum

	SettingSession     = settings.Session
	SettingReadOnly    = settings.ReadOnly
	SettingPrivileged  = settings.Privileged
	settingStartupOnly = settings.StartupOnly

	SettingUnitNone     = settings.UnitNone
	SettingBytes        = settings.Bytes
	SettingKilobytes    = settings.Kilobytes
	SettingMegabytes    = settings.Megabytes
	SettingMilliseconds = settings.Milliseconds
	SettingSeconds      = settings.Seconds
	SettingMinutes      = settings.Minutes
)

// ValidateSetting returns an owned definition without publishing partial state.
func ValidateSetting(d SettingDefinition) (SettingDefinition, error) { return settings.Validate(d) }
