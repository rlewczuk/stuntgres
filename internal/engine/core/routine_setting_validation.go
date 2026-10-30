package core

import (
	"errors"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ValidateRoutineSetting enters and returns with Engine.Mu held. Declaration
// admission and FROM CURRENT capture precede normalization; an external
// normalizer runs with all publication locks released. The suspended command
// resumes only against its exact live authority, never by replaying the callback.
func (h DDLHost) ValidateRoutineSetting(actor ddl.Authority, clause *parser.Set) (name string, value string, remove bool, err error) {
	auth := DDLAuthorityOf(actor)
	store := h.Op.EffectiveSettings()
	store.Mu.Lock()
	d, err := store.LookupDefinition(h.E.Settings, clause.Name.Text, !clause.FromCurrent)
	store.Mu.Unlock()
	if err != nil {
		return "", "", false, err
	}
	allowed := func(name string) (bool, error) { return parameterSetAuthority(auth, name) }
	if err := session.SettingAccess(d, allowed); err != nil {
		return "", "", false, err
	}
	// PostgreSQL's declaration array admission does not give unknown custom
	// placeholders the generic user-settable privilege of ordinary SET.
	if _, registered := h.E.Settings.Definitions()[d.Name]; !registered {
		ok, err := allowed(d.Name)
		if err != nil {
			return "", "", false, err
		}
		if !ok {
			return "", "", false, SQLError("42501", fmt.Sprintf(`permission denied to set parameter "%s"`, d.Name))
		}
	}
	if clause.Default {
		if err := settingActionEligibility(d, nil, session.SettingSet); err != nil {
			return "", "", false, err
		}
	}
	if clause.Default {
		return d.Name, "", true, nil
	}
	var input *string
	if clause.FromCurrent {
		if d.ReadRestricted {
			allowed, err := auth.canReadRestrictedSettings()
			if err != nil {
				return "", "", false, err
			}
			if err := session.SettingReadAccess(d, clause.Name.Text, allowed); err != nil {
				return "", "", false, err
			}
		}
		store.Mu.Lock()
		current := store.Value(d.Name)
		store.Mu.Unlock()
		input = &current
	} else {
		input, err = session.SetInput(h.Op, clause, false)
		if err != nil {
			return "", "", false, err
		}
	}
	var parsed settings.Value
	if d.Normalize == nil {
		parsed, _, err = store.ParseValue(d, *input)
	} else {
		// Keep the ordered attribute fold and its unpublished descriptor on the
		// stack. The live authority (not the stage's recaptured candidate view)
		// is the predecessor for every permission/identity decision already made.
		before, captureErr := h.E.AuthorizationLocked(h.Op)
		if captureErr != nil {
			return "", "", false, captureErr
		}
		scope := ActiveScopeOfLease(h.Op.Lease)
		if h.CatalogStage != nil {
			h.CatalogStage.RoutineSettingNormalized = true
		}
		if scope != nil {
			scope.releaseCatalogReservation()
		}
		h.E.Mu.Unlock()
		// Relock even if a native callback panics: the command's lock owner
		// still owns its matching unlock on every unwind path.
		func() {
			defer h.E.Mu.Lock()
			parsed, _, err = store.ParseValue(d, *input)
		}()
		// Cancellation, replacement and detached sessions precede continuation.
		store.Mu.Lock()
		h.Op.Session.Mu.Lock()
		admissionErr := store.AdmitLocked(DomainOf(h.E), h.Op)
		h.Op.Session.Mu.Unlock()
		store.Mu.Unlock()
		if admissionErr != nil {
			return "", "", false, admissionErr
		}
		if ActiveScopeOfLease(h.Op.Lease) != scope || (scope != nil && scope.Op != h.Op) ||
			h.Op.EffectiveSettings() != store || before.Catalog.Epoch() != h.E.Catalog.State.Epoch() {
			return "", "", false, ids.ErrCatalogReplaced
		}
		current, admissionErr := h.E.AuthorizationLocked(h.Op)
		if admissionErr != nil {
			return "", "", false, admissionErr
		}
		// Registry definitions are engine-owned and immutable. Placeholder
		// recognition and FROM CURRENT read eligibility, however, are live.
		store.Mu.Lock()
		_, admissionErr = store.LookupDefinition(h.E.Settings, clause.Name.Text, !clause.FromCurrent)
		store.Mu.Unlock()
		if admissionErr != nil {
			return "", "", false, admissionErr
		}
		liveAllowed := func(name string) (bool, error) { return parameterSetAuthority(current, name) }
		if admissionErr = session.SettingAccess(d, liveAllowed); admissionErr != nil {
			return "", "", false, admissionErr
		}
		if clause.FromCurrent && d.ReadRestricted {
			readAllowed, readErr := current.canReadRestrictedSettings()
			if readErr != nil {
				return "", "", false, readErr
			}
			if readErr = session.SettingReadAccess(d, clause.Name.Text, readAllowed); readErr != nil {
				return "", "", false, readErr
			}
		}
		// An authority/catalog change can invalidate any earlier owner, schema,
		// type or language admission. Reject rather than reconstructing the
		// declaration or invoking this external callback a second time.
		if current.Catalog.State() != before.Catalog.State() || current.Catalog.Domain() != before.Catalog.Domain() ||
			current.Catalog.Epoch() != before.Catalog.Epoch() || current.Revision != before.Revision ||
			current.RoleRecord != before.RoleRecord || current.State != before.State {
			return "", "", false, ids.ErrCatalogReplaced
		}
		auth = current
	}
	if err != nil {
		return "", "", false, err
	}
	if err := checkClientSettingAuthority(auth, d, &parsed); err != nil {
		return "", "", false, err
	}
	if d.Name == "client_encoding" {
		// Declaration checks resolve the actual pair without installing it in
		// the issuer's active policy, reset image or accepted-generation cache.
		if _, err := h.E.prepareClientEncodingLocked(h.Op, parsed.ClientEncoding); err != nil {
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) && diagnostic.Code == "0A000" {
				err = clientEncodingSettingError(parsed.ClientEncoding)
			}
			return "", "", false, err
		}
	}
	if isCurrentTransactionSetting(d.Name) {
		// GUCArrayAdd tests with PGC_S_TEST/ACTION_SET and changeVal=false:
		// check hooks still run, but neither assignments nor SAVE actions do.
		// Routine DDL has a catalog snapshot even when no data snapshot is
		// retained by this engine's transaction owner. Operation admission
		// serializes owner access; do not take its Mu under Engine.Mu.
		mode := session.CharacteristicMode(d.Name, d.Format(parsed))
		if err := h.Op.Session.Transactions.CheckModes([]parser.TransactionMode{mode}, true); err != nil {
			return "", "", false, err
		}
	}
	return settings.FoldName(d.Name), d.Format(parsed), false, nil
}

// ValidateRoutineConfiguration preflights validator SAVE actions only when
// checks are on at entry, then returns the scoped body policy after normalized
// configuration. Only the effective settings mutex is borrowed under Engine.Mu;
// no live settings or transaction journals are installed. Private replay keeps
// its separate source-only SQL and checked-PL admission contracts.
func (h DDLHost) ValidateRoutineConfiguration(d *catalog.Procedure) (bool, error) {
	if OperationReplayOrZero(h.Op).SQLBodies {
		return false, nil
	}
	store := h.Op.EffectiveSettings()
	store.Mu.Lock()
	entry := store.TypedValue("check_function_bodies").Boolean
	store.Mu.Unlock()
	if !entry {
		return false, nil
	}
	validateBody := true
	for i := 0; d.Configuration != nil && i < d.Configuration.Len(); i++ {
		name, input, valid := strings.Cut(d.Configuration.At(i), "=")
		if !valid {
			return false, fmt.Errorf("stuntgres: malformed routine configuration")
		}
		store.Mu.Lock()
		definition, err := store.LookupDefinition(h.E.Settings, name, true)
		store.Mu.Unlock()
		if err != nil {
			return false, err
		}
		if err := restrictedParameter(h.Op, name); err != nil {
			return false, err
		}
		if err := settingActionEligibility(definition, &input, session.SettingSave); err != nil {
			return false, err
		}
		if name == "check_function_bodies" {
			validateBody = input == "on"
		}
	}
	return validateBody, nil
}
