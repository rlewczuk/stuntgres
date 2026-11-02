package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func pgcryptoModuleOwner(t *testing.T) (*core.Engine, *session.Session, *session.Operation, authorization.PrincipalRef) {
	t.Helper()
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	op, err := s.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	t.Cleanup(op.Done)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	return e, s, op, auth.State.CurrentUser
}

func pgcryptoModuleAssign(t *testing.T, e *core.Engine, op *session.Operation, name, text string, action session.SettingAction, setter authorization.PrincipalRef) {
	t.Helper()
	store := op.EffectiveSettings()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	store.Mu.Lock()
	defer store.Mu.Unlock()
	d, err := store.LookupDefinition(e.Settings, name, true)
	if err != nil { t.Fatal(err) }
	value, err := d.Parse(text)
	if err != nil { t.Fatal(err) }
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := store.PgcryptoAssignmentHeadroomLocked(name, true); err != nil { t.Fatal(err) }
	if _, err := store.ApplyLocked(op, core.DomainOf(e), e.Settings, d, &value, nil, action); err != nil { t.Fatal(err) }
	store.CapturePgcryptoSetterLocked(name, setter, true)
}

func pgcryptoModuleInitialize(t *testing.T, e *core.Engine, s *session.Session, op *session.Operation, allow func(authorization.PrincipalRef) (bool, error), context string) []sqlstate.Error {
	t.Helper()
	s.Transactions.Mu.Lock()
	defer s.Transactions.Mu.Unlock()
	e.Mu.Lock()
	notices, err := s.Settings.InitializePgcrypto(op, core.DomainOf(e), e.Settings, allow, context)
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	return notices
}

func TestPgcryptoModulePromotion(t *testing.T) {
	for _, tc := range []struct{ name, input, want, code string; allowed bool }{
		{"Canonical", "OFF", "off", "", true}, {"Invalid", "bogus", "on", "22023", true}, {"OriginalSetter", "off", "on", "42501", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, op, setter := pgcryptoModuleOwner(t)
			peer := enginetest.StartSession(t, e, nil)
			registryDefaults := e.Settings.SessionDefaults()
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, tc.input, session.SettingSet, setter)
			s.Settings.FinishTransaction(true)
			pgcryptoModuleAssign(t, e, op, "pgcrypto.typo", "remove", session.SettingSet, setter)
			pgcryptoModuleAssign(t, e, op, "other_crypto.keep", "keep", session.SettingSet, setter)
			mark := s.Settings.Savepoint()
			before := mark.Clone()
			notices := pgcryptoModuleInitialize(t, e, s, op, func(original authorization.PrincipalRef) (bool, error) {
				if original != setter { t.Fatal("initializer substituted current bootstrap actor", original, setter) }
				return tc.allowed, nil
			}, "owned CREATE validation context")
			wantCount := 1
			if tc.code != "" { wantCount++ }
			if len(notices) != wantCount { t.Fatal("promotion notices", notices) }
			if tc.code != "" {
				message := `invalid value for parameter "pgcrypto.builtin_crypto_enabled": "bogus"`
				hint := "Available values: on, off, fips."
				if tc.code == "42501" { message, hint = `permission denied to set parameter "pgcrypto.builtin_crypto_enabled"`, "" }
				want := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: tc.code, Message: message, Hint: hint, Context: "owned CREATE validation context"}
				if notices[0] != want { t.Fatal("owned promotion diagnostic", notices[0], want) }
			}
			wantRemoval := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42602", Message: `invalid configuration parameter name "pgcrypto.typo", removing it`, Detail: `"pgcrypto" is now a reserved prefix.`, Context: "owned CREATE validation context"}
			if notices[len(notices)-1] != wantRemoval { t.Fatal("reserved prefix diagnostic", notices) }
			s.Settings.Mu.Lock()
			d, err := s.Settings.LookupDefinition(e.Settings, settings.PgcryptoBuiltinCrypto, false)
			value, reset := s.Settings.Value(d.Name), s.Settings.DefaultTyped[d.Name]
			_, reservedErr := s.Settings.LookupDefinition(e.Settings, "pgcrypto.typo", true)
			_, removedErr := s.Settings.LookupDefinition(e.Settings, "pgcrypto.typo", false)
			unrelated := s.Settings.Value("other_crypto.keep")
			s.Settings.Mu.Unlock()
			if err != nil || d.Type != settings.Enum || d.Access != settings.Privileged || value != tc.want || reset.Text != "on" || unrelated != "keep" { t.Fatal("coherent promoted state", d, value, reset, unrelated, err) }
			var diagnostic *sqlstate.Error
			if !errors.As(reservedErr, &diagnostic) || diagnostic.Code != "42602" || diagnostic.Detail != `"pgcrypto" is a reserved prefix.` { t.Fatal(reservedErr) }
			if !errors.As(removedErr, &diagnostic) || diagnostic.Code != "42704" { t.Fatal(removedErr) }
			if !reflect.DeepEqual(mark, before) || mark.Values[settings.PgcryptoBuiltinCrypto] != tc.input { t.Fatal("promotion mutated retained untyped image") }
			if _, found := registryDefaults.Text[settings.PgcryptoBuiltinCrypto]; found { t.Fatal("promotion mutated shared reset backing") }
			if _, found := peer.Setting(settings.PgcryptoBuiltinCrypto); found || peer.Settings.PgcryptoInitialized() { t.Fatal("promotion activated a physical peer") }
			if notices := pgcryptoModuleInitialize(t, e, s, op, func(authorization.PrincipalRef) (bool, error) { t.Fatal("replayed initialized module"); return false, nil }, "wrong later context"); len(notices) != 0 { t.Fatal("duplicate initialization notices", notices) }
		})
	}
	t.Run("DistinctReplayActionsDoNotCollapse", func(t *testing.T) {
		e, s, op, setter := pgcryptoModuleOwner(t)
		pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "off", session.SettingSet, setter)
		s.Settings.FinishTransaction(true)
		s.Settings.NestLevel = 1
		pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "bogus", session.SettingLocal, setter)
		s.Settings.NestLevel = 2
		pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "bogus", session.SettingLocal, setter)
		notices := pgcryptoModuleInitialize(t, e, s, op, func(authorization.PrincipalRef) (bool, error) { return true, nil }, "")
		if len(notices) != 2 || notices[0].Code != "22023" || notices[1] != notices[0] { t.Fatal("distinct failed LOCAL replay actions", notices) }
		if value, _ := s.Setting(settings.PgcryptoBuiltinCrypto); value != "off" { t.Fatal("failed replay overwrote prior valid value", value) }
		s.Settings.FinishTransaction(true)
		if value, _ := s.Setting(settings.PgcryptoBuiltinCrypto); value != "off" { t.Fatal("failed replay created a commit action", value) }
	})
	for _, restricted := range []bool{false, true} {
		name := "ActiveSavepoint"
		if restricted { name = "ActiveInvocationSavepoint" }
		t.Run(name, func(t *testing.T) {
			e, s, op, setter := pgcryptoModuleOwner(t)
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "off", session.SettingSet, setter)
			s.Settings.FinishTransaction(true)
			s.Settings.NestLevel = 1
			if restricted { op.Restricted = &session.RestrictedInvocation{Owner: setter} }
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "bogus", session.SettingLocal, setter)
			owner := s.Transactions
			owner.Mu.Lock()
			mark := owner.CaptureSavepoint(session.SavepointMark{Name: "active-policy", Settings: s.Settings.Savepoint(), Invocation: op.CaptureInvocation(), Identity: s.Authorization.State})
			owner.Savepoints = append(owner.Savepoints, mark)
			original := owner.Savepoints
			owner.Mu.Unlock()
			activeImage := op.EffectiveSettings()
			if activeImage.Values[settings.PgcryptoBuiltinCrypto] != "bogus" { t.Fatal("missing raw preload witness") }
			control := op.CaptureInvocation() // Independent, unregistered pre-load image.
			if !reflect.DeepEqual(mark.Invocation, control) { t.Fatal("checkpoint capture changed the raw invocation image") }
			// Keep the real flat journal level, exactly as SQL SAVEPOINT does.
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "bogus", session.SettingLocal, setter)
			notices := pgcryptoModuleInitialize(t, e, s, op, func(authorization.PrincipalRef) (bool, error) { return true, nil }, "activation scope")
			if len(notices) != 2 || notices[0].Code != "22023" || notices[0].Context != "activation scope" || notices[1] != notices[0] { t.Fatal("active historical/current actions", notices) }
			published := owner.Savepoints[0]
			if &owner.Savepoints[0] == &original[0] || published.Serial != mark.Serial || published.Name != mark.Name { t.Fatal("promotion mutated retained savepoint backing/identity") }
			if restricted {
				if published.Invocation == mark.Invocation { t.Fatal("promotion reused retained invocation checkpoint") }
				// Restore refreshes typed settings and legitimately promotes its
				// now-current image. Compare old ownership before consuming only
				// the published checkpoint, never by restoring the retained one.
				if !reflect.DeepEqual(mark.Invocation, control) { t.Fatal("promotion mutated retained invocation image") }
				if reflect.DeepEqual(published.Invocation, control) { t.Fatal("published invocation lacks typed promotion") }
				published.Invocation.Restore()
				if op.EffectiveSettings() == activeImage { t.Fatal("published invocation aliases live pre-promotion image") }
			} else {
				if original[0].Settings.Values[settings.PgcryptoBuiltinCrypto] != "bogus" { t.Fatal("promotion mutated retained settings image") }
				candidate := published.Settings.Clone()
				s.Settings.Mu.Lock()
				s.Mu.Lock()
				s.Settings.RestoreSavepointLocked(candidate, s.Authorization.State)
				s.Mu.Unlock()
				s.Settings.Mu.Unlock()
			}
			if value, _ := op.Setting(settings.PgcryptoBuiltinCrypto); value != "off" { t.Fatal("active mark restored failed untyped assignment", value) }
			if restricted && !reflect.DeepEqual(mark.Invocation, control) { t.Fatal("restoration mutated retained invocation image") }
			if notices := pgcryptoModuleInitialize(t, e, s, op, func(authorization.PrincipalRef) (bool, error) { t.Fatal("rechecked initialized authority"); return false, nil }, ""); len(notices) != 0 { t.Fatal("restore repeated promotion notices", notices) }
			op.Restricted = nil
		})
	}
	t.Run("CapturedResetAndRetainedMark", func(t *testing.T) {
		e, s, op, setter := pgcryptoModuleOwner(t)
		s.Settings.Mu.Lock()
		err := s.Settings.CapturePersistedDefaults(t.Context(), e.Settings, map[string]session.CapturedDefault{settings.PgcryptoBuiltinCrypto: {Input: "off", Source: session.DefaultSourceUser}})
		s.Settings.Mu.Unlock()
		if err != nil { t.Fatal(err) }
		pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "bogus", session.SettingSet, setter)
		s.Settings.FinishTransaction(true)
		mark := s.Settings.Savepoint()
		notices := pgcryptoModuleInitialize(t, e, s, op, func(authorization.PrincipalRef) (bool, error) { return true, nil }, "")
		if len(notices) != 1 || notices[0].Code != "22023" { t.Fatal(notices) }
		candidate := mark.Clone()
		s.Settings.Mu.Lock()
		s.Mu.Lock()
		s.Settings.RestoreSavepointLocked(candidate, s.Authorization.State)
		s.Mu.Unlock()
		d, found := s.Settings.EffectiveDefinitionLocked(e.Settings, settings.PgcryptoBuiltinCrypto)
		value, reset, source := s.Settings.Value(settings.PgcryptoBuiltinCrypto), s.Settings.DefaultTyped[settings.PgcryptoBuiltinCrypto], s.Settings.DefaultSources[settings.PgcryptoBuiltinCrypto]
		s.Settings.Mu.Unlock()
		if !found || d.Type != settings.Enum || value != "off" || reset.Text != "off" || source != session.DefaultSourceUser { t.Fatal("typed mark lost captured reset/provenance", d, value, reset, source) }
		if mark.Values[settings.PgcryptoBuiltinCrypto] != "bogus" { t.Fatal("conversion mutated retained untyped mark") }
	})
	t.Run("RegisteredSiblingSurvivesMarkPromotion", func(t *testing.T) {
		const name = "pgcrypto.registered"
		d, err := settings.Validate(settings.Definition{Name: name, Type: settings.String, Access: settings.Session, BootDefault: "boot"})
		if err != nil { t.Fatal(err) }
		e := rootengine.NewWithSettings([]settings.Definition{d}, "", false)
		s := enginetest.StartSession(t, e, nil)
		op, err := s.Acquire(t.Context())
		if err != nil { t.Fatal(err) }
		t.Cleanup(op.Done)
		pgcryptoModuleAssign(t, e, op, name, "session", session.SettingSet, authorization.PrincipalRef{})
		s.Settings.FinishTransaction(true)
		s.Settings.NestLevel = 1
		pgcryptoModuleAssign(t, e, op, name, "local", session.SettingLocal, authorization.PrincipalRef{})
		mark := s.Settings.Savepoint()
		if notices := pgcryptoModuleInitialize(t, e, s, op, func(authorization.PrincipalRef) (bool, error) { return true, nil }, ""); len(notices) != 0 { t.Fatal(notices) }
		candidate := mark.Clone()
		s.Settings.Mu.Lock()
		s.Mu.Lock()
		s.Settings.RestoreSavepointLocked(candidate, s.Authorization.State)
		s.Mu.Unlock()
		registered, found := s.Settings.EffectiveDefinitionLocked(e.Settings, name)
		s.Settings.Mu.Unlock()
		if value, _ := s.Setting(name); value != "local" || !found || registered.Type != settings.String { t.Fatal("reserved prefix removed genuine definition/override", value, registered) }
		s.Settings.FinishTransaction(false)
		if value, _ := s.Setting(name); value != "session" { t.Fatal("reserved prefix removed genuine journal original", value) }
		if value, ok := e.Settings.Definitions()[name]; !ok || value.BootValue().Text != "boot" { t.Fatal("promotion changed immutable registry", value) }
	})
	t.Run("RemovedPlaceholderActionCannotReappear", func(t *testing.T) {
		const name = "pgcrypto.removed"
		e, s, op, setter := pgcryptoModuleOwner(t)
		pgcryptoModuleAssign(t, e, op, name, "old", session.SettingSet, setter)
		s.Settings.FinishTransaction(true)
		s.Settings.NestLevel = 1
		e.Mu.Lock()
		s.Settings.Mu.Lock()
		s.Mu.Lock()
		d, err := s.Settings.LookupDefinition(e.Settings, name, false)
		if err == nil { _, err = s.Settings.ApplyLocked(op, core.DomainOf(e), e.Settings, d, nil, nil, session.SettingSet) }
		s.Mu.Unlock()
		s.Settings.Mu.Unlock()
		e.Mu.Unlock()
		if err != nil { t.Fatal(err) }
		if _, present := s.Settings.Values[name]; present { t.Fatal("RESET witness must have only a journal original") }
		mark := s.Settings.Savepoint()
		if notices := pgcryptoModuleInitialize(t, e, s, op, func(authorization.PrincipalRef) (bool, error) { return true, nil }, ""); len(notices) != 1 || notices[0].Code != "42602" { t.Fatal(notices) }
		candidate := mark.Clone()
		s.Settings.Mu.Lock()
		s.Mu.Lock()
		s.Settings.RestoreSavepointLocked(candidate, s.Authorization.State)
		s.Mu.Unlock()
		s.Settings.Mu.Unlock()
		s.Settings.FinishTransaction(false)
		if value, recognized := s.Setting(name); recognized { t.Fatal("rollback resurrected reserved placeholder", value) }
		if _, present := s.Settings.Actions[name]; present { t.Fatal("promotion retained removed placeholder action") }
	})
	t.Run("ActualModeTruthTable", func(t *testing.T) {
		e, s, op, setter := pgcryptoModuleOwner(t)
		pgcryptoModuleInitialize(t, e, s, op, func(authorization.PrincipalRef) (bool, error) { return true, nil }, "")
		for _, policy := range []string{"on", "off", "fips"} {
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, policy, session.SettingSet, setter)
			for _, actual := range []bool{false, true} {
				err := s.Settings.CheckPgcryptoBuiltinCrypto(actual)
				deny := policy == "off" || policy == "fips" && actual
				if !deny { if err != nil { t.Fatal(policy, actual, err) }; continue }
				want := "use of built-in crypto functions is disabled"
				if policy == "fips" { want = "use of non-FIPS validated crypto not allowed when OpenSSL is in FIPS mode" }
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != want { t.Fatal(policy, actual, err) }
			}
		}
	})
	t.Run("CanceledTransitionDoesNotPublish", func(t *testing.T) {
		e := rootengine.New()
		s := enginetest.StartSession(t, e, nil)
		ctx, cancel := context.WithCancel(t.Context())
		op, err := s.Acquire(ctx)
		if err != nil { t.Fatal(err) }
		defer op.Done()
		cancel()
		s.Transactions.Mu.Lock()
		e.Mu.Lock()
		_, err = s.Settings.InitializePgcrypto(op, core.DomainOf(e), e.Settings, func(authorization.PrincipalRef) (bool, error) { return true, nil }, "")
		e.Mu.Unlock()
		s.Transactions.Mu.Unlock()
		if !errors.Is(err, context.Canceled) || s.Settings.PgcryptoInitialized() { t.Fatal("canceled registration published", err) }
	})
}

func TestPgcryptoModuleJournal(t *testing.T) {
	e, s, op, setter := pgcryptoModuleOwner(t)
	pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "off", session.SettingSet, setter)
	s.Settings.FinishTransaction(true)
	s.Settings.NestLevel = 1
	pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "fips", session.SettingLocal, setter)
	mark := s.Settings.Savepoint()
	s.Settings.NestLevel = 2
	pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "on", session.SettingLocal, setter)
	pgcryptoModuleInitialize(t, e, s, op, func(authorization.PrincipalRef) (bool, error) { return true, nil }, "")
	candidate := mark.Clone() // Promotion allocates before the final restore window.
	s.Settings.Mu.Lock()
	s.Mu.Lock()
	s.Settings.RestoreSavepointLocked(candidate, s.Authorization.State)
	s.Mu.Unlock()
	s.Settings.Mu.Unlock()
	if value, ok := s.Setting(settings.PgcryptoBuiltinCrypto); !ok || value != "fips" || !s.Settings.PgcryptoInitialized() { t.Fatal("typed savepoint restoration", value, ok) }
	s.Settings.FinishTransaction(false)
	if value, _ := s.Setting(settings.PgcryptoBuiltinCrypto); value != "off" || !s.Settings.PgcryptoInitialized() { t.Fatal("rollback unloaded/lost original", value) }
	// A pre-load exception image and detached invocation share physical module
	// lifetime, but not overrides, defaults or mutable action maps.
	e2, physical, operation, actor := pgcryptoModuleOwner(t)
	pgcryptoModuleAssign(t, e2, operation, settings.PgcryptoBuiltinCrypto, "off", session.SettingSet, actor)
	physical.Settings.FinishTransaction(true)
	operation.Restricted = &session.RestrictedInvocation{Owner: actor}
	image := operation.EffectiveSettings()
	pgcryptoModuleAssign(t, e2, operation, settings.PgcryptoBuiltinCrypto, "on", session.SettingSave, actor)
	exception := operation.CaptureInvocation()
	pgcryptoModuleInitialize(t, e2, physical, operation, func(authorization.PrincipalRef) (bool, error) { return true, nil }, "")
	image.Mu.Lock()
	imageValue := image.Value(settings.PgcryptoBuiltinCrypto)
	image.Mu.Unlock()
	if imageValue != "on" { t.Fatal("initializer lost invocation override", imageValue) }
	exception.Restore()
	if value, _ := operation.Setting(settings.PgcryptoBuiltinCrypto); value != "on" { t.Fatal("exception restored unpromoted image", value) }
	operation.Restricted = nil
	if value, _ := operation.Setting(settings.PgcryptoBuiltinCrypto); value != "off" || !physical.Settings.PgcryptoInitialized() { t.Fatal("invocation changed physical policy/lifetime", value) }
	if err := physical.Settings.EndSession(physical.SessionToken, session.CleanClose); err != nil || physical.Settings.PgcryptoInitialized() || physical.Settings.DefaultTyped != nil { t.Fatal("physical module cleanup", err) }
}
