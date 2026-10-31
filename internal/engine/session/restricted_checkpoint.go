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
		clientEncodingOwner:  source.clientEncodingAuthority(),
		TempTablespaces:      cloneTempTablespaceAssignment(source.TempTablespaces, source.Value("temp_tablespaces")),
		statsOwner:           source.statsAuthority(),
		Defaults:             maps.Clone(source.Defaults), Values: maps.Clone(source.Values),
		Typed: maps.Clone(source.Typed), DefaultTyped: maps.Clone(source.DefaultTyped),
		Placeholders: maps.Clone(source.Placeholders), ProviderDefaults: maps.Clone(source.ProviderDefaults),
		DefaultSources: maps.Clone(source.DefaultSources),
		Actions:        maps.Clone(source.Actions), RoleAction: source.RoleAction, NestLevel: source.NestLevel,
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
	beforeRole := m.operation.EffectiveSettings().TypedValue("session_replication_role").Text
	beforeStats := m.operation.EffectiveSettings().Value("stats_fetch_consistency")
	m.frame.settings, m.frame.SafePath = m.image, m.safePath
	m.image.RuleRoleChanged(beforeRole)
	m.image.StatsChanged(beforeStats)
	m.operation.RefreshInvocationSettingsLocked(m.image)
}
