package session

import (
	"context"
	"errors"
)

func (s *SettingsStore) overexplainAuthority() *SettingsStore {
	if s.overexplainOwner != nil {
		return s.overexplainOwner
	}
	return s
}

// OverexplainLoaded reads original physical authority, not a journaled image.
func (s *SettingsStore) OverexplainLoaded() bool {
	return s.overexplainAuthority().overexplainLoaded.Load()
}

// LoadOverexplain is entered only after fresh LOAD authorization. The physical
// mutex serializes activation against terminal cleanup; no setting is installed.
func (s *SettingsStore) LoadOverexplain(ctx context.Context) error {
	owner := s.overexplainAuthority()
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner.Ended {
		return errors.New("stuntgres: session has ended")
	}
	owner.overexplainLoaded.Store(true)
	return nil
}
