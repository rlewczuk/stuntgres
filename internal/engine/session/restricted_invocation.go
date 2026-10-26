package session

import (
	"stuntgres/internal/engine/authorization"
)

// RestrictedInvocation belongs to one serial engine invocation, outside lexical
// routine scratch. The stack is not session identity or transaction state.
type RestrictedInvocation struct {
	Owner    authorization.PrincipalRef
	SafePath bool // Initial fixed lookup path; ordinary later SET may replace it locally.
	Parent   *RestrictedInvocation
	settings *SettingsStore
}

// EffectiveSettings is the canonical operation settings route. The detached
// image is made on first demand, inheriting a parent invocation's effective
// values without sharing mutable maps, actions, placeholders or live timers.
func (op *Operation) EffectiveSettings() *SettingsStore {
	frame := op.Restricted
	if frame == nil {
		return op.Session.Settings
	}
	if frame.settings != nil {
		return frame.settings
	}
	op.Restricted = frame.Parent
	source := op.EffectiveSettings()
	op.Restricted = frame
	frame.settings = cloneInvocationSettings(source)
	return frame.settings
}

func (op *Operation) Setting(name string) (string, bool) {
	store := op.EffectiveSettings()
	store.Mu.Lock()
	defer store.Mu.Unlock()
	_, hasDefault := store.Defaults[name]
	_, hasValue := store.Values[name]
	if !hasDefault && !hasValue && !store.Placeholders[name] {
		return "", false
	}
	return store.Value(name), true
}
