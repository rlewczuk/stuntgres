package session

import "maps"

// InvocationMark belongs only to an internal procedural savepoint. Ordinary
// transaction marks never carry one or redirect their session restoration.
type InvocationMark struct {
	frame     *RestrictedInvocation
	operation *Operation
	image     *SettingsStore
	safePath  bool
}

func cloneInvocationSettings(source *SettingsStore) *SettingsStore {
	source.Mu.Lock()
	defer source.Mu.Unlock()
	return &SettingsStore{
		Session: source.Session, Invocation: true,
		temporaryBufferOwner: source.temporaryBufferAuthority(),
		Defaults:             maps.Clone(source.Defaults), Values: maps.Clone(source.Values),
		Typed: maps.Clone(source.Typed), DefaultTyped: maps.Clone(source.DefaultTyped),
		Placeholders: maps.Clone(source.Placeholders), ProviderDefaults: maps.Clone(source.ProviderDefaults),
		Actions: maps.Clone(source.Actions), RoleAction: source.RoleAction,
		CurrentIsolation: source.CurrentIsolation, CurrentReadOnly: source.CurrentReadOnly, CurrentDeferrable: source.CurrentDeferrable,
	}
}
func (op *Operation) CaptureInvocation() *InvocationMark {
	if op.Restricted == nil {
		return nil
	}
	return &InvocationMark{frame: op.Restricted, operation: op, image: cloneInvocationSettings(op.EffectiveSettings()), safePath: op.Restricted.SafePath}
}
func (m *InvocationMark) Restore() {
	// One unnamed exception checkpoint is consumed once. Its complete image is
	// independent of its frame, including recognition and setting action state.
	m.frame.settings, m.frame.SafePath = m.image, m.safePath
	m.operation.RefreshInvocationSettingsLocked(m.image)
}
