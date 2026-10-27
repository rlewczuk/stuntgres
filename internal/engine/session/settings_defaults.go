package session

import (
	"context"
	"maps"

	"stuntgres/internal/engine/settings"
)

// DefaultSource is frozen with the physical session's typed reset image.
type DefaultSource uint8

const (
	DefaultSourceDefault DefaultSource = iota
	DefaultSourceGlobal
	DefaultSourceDatabase
	DefaultSourceUser
	DefaultSourceDatabaseUser
	DefaultSourceClient
)

func (s DefaultSource) String() string {
	return [...]string{"default", "global", "database", "user", "database user", "client"}[s]
}

type CapturedDefault struct {
	Input  string
	Source DefaultSource
}

// CapturePersistedDefaults consumes an already selected immutable startup image.
// Initialize has captured providers first: a present response wins even if equal
// to the registry default. No callbacks, authorization checks or live installs
// occur here; administrator-authored defaults do not require the login's SET ACL.
func (s *SettingsStore) CapturePersistedDefaults(ctx context.Context, registry settings.Registry, captured map[string]CapturedDefault) error {
	for name, entry := range captured {
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "role" || s.ProviderDefaults[name] {
			continue
		}
		d, err := s.LookupDefinition(registry, name, true)
		if err != nil {
			return err
		}
		v, _, err := s.ParseValue(d, entry.Input)
		if err != nil {
			return err
		}
		if s.defaultsShared {
			s.Defaults = maps.Clone(s.Defaults)
			s.DefaultTyped = maps.Clone(s.DefaultTyped)
			s.defaultsShared = false
		}
		if _, registered := registry.Definitions()[name]; !registered {
			s.Placeholders[name] = true
		}
		s.Defaults[name], s.DefaultTyped[name] = d.Format(v), v
		if s.DefaultSources == nil {
			s.DefaultSources = make(map[string]DefaultSource)
		}
		s.DefaultSources[name] = entry.Source
	}
	return ctx.Err()
}
