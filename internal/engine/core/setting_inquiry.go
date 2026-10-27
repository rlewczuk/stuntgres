package core

import "stuntgres/internal/engine/session"

// settingValue reads actor-dependent settings from the same effective authority
// as current_user. Superuser status is a role attribute, never inherited through
// membership and never installed in the session settings journal.
func (a AuthorizationView) settingValue(store *session.SettingsStore, name string) (string, error) {
	if name != "is_superuser" {
		return store.Value(name), nil
	}
	role, err := a.Roles.Descriptor(a.State.CurrentUser)
	if err != nil {
		return "", err
	}
	if role.Superuser {
		return "on", nil
	}
	return "off", nil
}
