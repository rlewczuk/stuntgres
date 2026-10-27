package session

import "time"

// BeginSettingsSaveLocked creates a GUC nesting level, not a transaction or a
// snapshot. Only subsequently touched variables acquire entries at this level.
func (s *SettingsStore) BeginSettingsSaveLocked() int {
	if s.NestLevel < 1 {
		s.NestLevel = 1
	}
	s.NestLevel++
	return s.NestLevel
}

// EndSettingsSaveLocked follows the SET/LOCAL/SAVE merge rules. Caller holds
// Settings.Mu then Session.Mu. Restoring captured values never checks privileges
// or reruns parsers/providers. Immutable tails survive exception checkpoints.
func (s *SettingsStore) EndSettingsSaveLocked(level int, commit bool) {
	for name, a := range s.Actions {
		for a.level >= level {
			if !commit || a.kind == settingSave {
				s.Restore(name, a.original)
				if a.previous == nil {
					delete(s.Actions, name)
					break
				}
				a = *a.previous
				s.Actions[name] = a
				continue
			}
			if a.previous == nil || a.previous.level < a.level-1 {
				a.level--
				s.Actions[name] = a
				continue
			}
			previous := *a.previous
			switch a.kind {
			case settingSession:
				previous.kind, previous.candidate = settingSession, settingOverride{}
			case settingLocal:
				if previous.kind == settingSession {
					previous.kind, previous.candidate = settingSessionLocal, a.original
					if !previous.candidate.Present {
						previous.candidate = settingOverride{Present: true, Text: s.Defaults[name], Typed: s.DefaultTyped[name]}
					}
				}
			case settingSessionLocal:
				previous.kind, previous.candidate = settingSessionLocal, a.candidate
			}
			a = previous
			s.Actions[name] = a
		}
	}
	s.NestLevel = level - 1
	if !s.Invocation && s.Session != nil {
		s.Session.PublishTimeoutSettingsLocked(s, false, time.Now())
	}
}
