package core

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
)

func unchangedRoutineEnvironment(err error) error { return err }

// enterRoutineEnvironment runs after reached caller admission and strictness,
// before SQL, PL or installed native trigger dispatch. It borrows the operation,
// and setting journal: no transaction completion, safe path or detached store.
func (s *StatementScope) enterRoutineEnvironment(d *catalog.Procedure) (leave func(error) error, err error) {
	if !d.SecurityDefiner && d.Configuration == nil {
		return unchangedRoutineEnvironment, nil
	}
	op, e := s.Op, s.Engine()
	previous := op.EffectiveUser
	identity, inquiry, authority := s.Identity, s.Inquiry, s.RoutineAuth
	input, output, presentation := s.Input, s.Output, s.presentationSlot
	rowSecurity := s.RowSecurity
	foreignActor := s.ForeignActionAuth
	restricted := op.Restricted
	safePath := restricted != nil && restricted.SafePath
	var entryPath string
	if safePath && d.Configuration != nil {
		entryPath, _ = op.Setting("search_path")
	}
	if d.SecurityDefiner {
		op.EffectiveUser = &session.EffectiveUserInvocation{Owner: d.Owner, Parent: previous, Restricted: op.Restricted}
		// Definer entry supersedes an enclosing RI action. Later actions in
		// this body may install their own child-owner authority as usual.
		s.ForeignActionAuth = nil
	}
	level := 0
	if d.Configuration != nil {
		store := op.EffectiveSettings()
		store.Mu.Lock()
		level = store.BeginSettingsSaveLocked()
		store.Mu.Unlock()
	}
	leave = func(primary error) error {
		if level != 0 {
			// A caught restricted subtransaction may replace its local image.
			store := op.EffectiveSettings()
			store.Mu.Lock()
			op.Session.Mu.Lock()
			store.EndSettingsSaveLocked(level, primary == nil)
			if safePath && op.Restricted == restricted && store.Value("search_path") == entryPath {
				restricted.SafePath = true
			}
			op.RefreshInvocationSettingsLocked(store)
			op.Session.Mu.Unlock()
			store.Mu.Unlock()
		}
		op.EffectiveUser = previous
		s.ForeignActionAuth = foreignActor
		// Parallel evaluation already owns closed caller facts. Cancellation
		// must restore those exact owners, not rebuild them from readable
		// authorization. Ordinary serial leave instead retains live recapture:
		// trigger entry may have demanded caller resolvers absent before entry.
		// Lexical SQL invocation ownership alone does not close that contract.
		parallel := exec.InParallelOperation(s.executionContext())
		var cleanup error
		if parallel {
			cleanup = op.Context().Err()
			if cleanup == nil {
				cleanup = s.executionContext().Err()
			}
		}
		if cleanup == nil {
			s.presentationSlot = nil
			cleanup = s.refreshRestrictedPresentation()
			// Cancellation during a successful capture also restores the exact
			// original owners, rather than retaining newly built resolver paths.
			if cleanup == nil && parallel {
				cleanup = op.Context().Err()
			}
			if cleanup == nil && parallel {
				cleanup = s.executionContext().Err()
			}
		}
		if cleanup != nil {
			// A canceled publication can refuse recapture; never retain the
			// callee's cached identity/authority on this error path.
			s.Identity, s.Inquiry, s.RoutineAuth = identity, inquiry, authority
			s.Input, s.Output, s.presentationSlot = input, output, presentation
			s.RowSecurity = rowSecurity
		}
		if primary != nil {
			return primary
		}
		return cleanup
	}
	s.presentationSlot = nil
	if err := s.refreshRestrictedPresentation(); err != nil {
		return nil, leave(err)
	}
	if d.Configuration != nil {
		for i := range d.Configuration.Len() {
			name, input, valid := strings.Cut(d.Configuration.At(i), "=")
			if !valid {
				return nil, leave(fmt.Errorf("stuntgres: malformed routine configuration"))
			}
			store := op.EffectiveSettings()
			e.Mu.Lock()
			store.Mu.Lock()
			definition, err := store.LookupDefinition(e.Settings, name, true)
			store.Mu.Unlock()
			e.Mu.Unlock()
			if err != nil {
				return nil, leave(err)
			}
			if _, err := e.applySetting(s.executionContext(), op, store, definition, &input, session.SettingSave, ""); err != nil {
				return nil, leave(err)
			}
		}
		// Entry owns the final presentation even when no routine command is
		// active yet; setting installation's nested-command refresh is not
		// the entry boundary.
		s.presentationSlot = nil
		if err := s.refreshRestrictedPresentation(); err != nil {
			return nil, leave(err)
		}
	}
	return leave, nil
}
