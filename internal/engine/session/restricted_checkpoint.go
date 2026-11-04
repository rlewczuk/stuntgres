package session

import "maps"

// InvocationMark belongs only to an internal procedural savepoint. Ordinary
// transaction marks never carry one or redirect their session restoration.
type InvocationMark struct {
	frame     *RestrictedInvocation
	operation *Operation
	image     *SettingsStore
	prepared  *SettingsStore
	safePath  bool
}

func cloneInvocationSettings(source *SettingsStore) *SettingsStore {
	source.Mu.Lock()
	defer source.Mu.Unlock()
	source.syncPgcryptoLocked(nil)
	image := cloneInvocationSettingsLocked(source)
	image.pgTrgmParent = source
	image.autoExplainParent = source
	return image
}

// Caller holds source.Mu. Activation uses this before publishing loaded, so
// cloning a retained exception image cannot silently consume its diagnostics.
// Preserve its ancestry; only a new active invocation adds source as a parent.
func cloneInvocationSettingsLocked(source *SettingsStore) *SettingsStore {
	image := &SettingsStore{
		Session: source.Session, Invocation: true,
		temporaryBufferOwner: source.temporaryBufferAuthority(),
		clientEncodingOwner:  source.clientEncodingAuthority(),
		isnOwner:             source.isnAuthority(),
		isnPromoted:          source.isnPromoted,
		overexplainOwner:     source.overexplainAuthority(),
		pgTrgmOwner:          source.pgTrgmAuthority(),
		pgTrgmParent:         source.pgTrgmParent,
		pgTrgmPromoted:       source.pgTrgmPromoted,
		autoExplainOwner:     source.autoExplainAuthority(),
		autoExplainParent:    source.autoExplainParent,
		autoExplainPromoted:  source.autoExplainPromoted,
		autoExplainSetters:   maps.Clone(source.autoExplainSetters),
		pgcryptoOwner:        source.pgcryptoPhysicalOwner(),
		pgcryptoPromoted:     source.pgcryptoPromoted,
		pgcryptoSetter:       source.pgcryptoSetter,
		pgcryptoAssignment:   source.pgcryptoAssignment,
		TempTablespaces:      cloneTempTablespaceAssignment(source.TempTablespaces, source.Value("temp_tablespaces")),
		statsOwner:           source.statsAuthority(),
		RoleAction:           source.RoleAction, NestLevel: source.NestLevel,
		CurrentIsolation: source.CurrentIsolation, CurrentReadOnly: source.CurrentReadOnly, CurrentDeferrable: source.CurrentDeferrable,
	}
	if !source.pgTrgmPromoted && source.pgTrgmAuthority().pgTrgmLoaded.Load() {
		candidate, _ := preparePgTrgmSettings(source, false)
		image.installPgTrgm(candidate)
	} else {
		image.Defaults, image.DefaultTyped = maps.Clone(source.Defaults), maps.Clone(source.DefaultTyped)
		image.Values, image.Typed = maps.Clone(source.Values), maps.Clone(source.Typed)
		image.Placeholders, image.Actions = maps.Clone(source.Placeholders), maps.Clone(source.Actions)
		image.ProviderDefaults, image.DefaultSources = maps.Clone(source.ProviderDefaults), maps.Clone(source.DefaultSources)
	}
	image.reconcileAutoExplainLocked()
	return image
}
func (op *Operation) CaptureInvocation() *InvocationMark {
	if op.Restricted == nil {
		return nil
	}
	source := op.EffectiveSettings()
	image := cloneInvocationSettings(source)
	image.pgTrgmParent = source.pgTrgmParent
	image.autoExplainParent = source.autoExplainParent
	return &InvocationMark{frame: op.Restricted, operation: op, image: image, safePath: op.Restricted.SafePath}
}

// PrepareRestore reconciles a retained exception image into an independent
// typed candidate before the final callback-free publication window.
func (m *InvocationMark) PrepareRestore() {
	m.prepared = cloneInvocationSettings(m.image)
	m.prepared.pgTrgmParent = m.image.pgTrgmParent
	m.prepared.autoExplainParent = m.image.autoExplainParent
}
func (m *InvocationMark) Restore() {
	// One unnamed exception checkpoint is consumed once. Its complete image is
	// independent of its frame, including recognition and setting action state.
	if m.prepared != nil {
		m.image = m.prepared
		m.prepared = nil
	}
	beforeRole := m.operation.EffectiveSettings().TypedValue("session_replication_role").Text
	beforeStats := m.operation.EffectiveSettings().Value("stats_fetch_consistency")
	m.frame.settings, m.frame.SafePath = m.image, m.safePath
	m.image.RuleRoleChanged(beforeRole)
	m.image.StatsChanged(beforeStats)
	m.operation.RefreshInvocationSettingsLocked(m.image)
}
