package enginetests_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func TestTempTablespacesCursor(t *testing.T) {
	var cursor session.TempTablespaceSelector
	cursor.Install(ids.TxRef{}, nil, nil)
	for range 3 {
		if cursor.Next() != 0 { t.Fatal("empty list did not select database default") }
	}
	cursor.Install(ids.TxRef{}, nil, []ids.OID{1664})
	for range 3 {
		if cursor.Next() != 1664 { t.Fatal("singleton changed selection") }
	}
	// The initial phase is unspecified. Every subsequent destination has an
	// exact independent expectation for each of the three possible phases.
	cursor.Install(ids.TxRef{}, nil, []ids.OID{1663, 1664, 0})
	first := cursor.Next()
	want := map[ids.OID][]ids.OID{
		1663: {1664, 0, 1663, 1664, 0, 1663, 1664, 0},
		1664: {0, 1663, 1664, 0, 1663, 1664, 0, 1663},
		0: {1663, 1664, 0, 1663, 1664, 0, 1663, 1664},
	}[first]
	if want == nil { t.Fatal("nonmember initial choice", first) }
	for _, expected := range want {
		if got := cursor.Next(); got != expected { t.Fatalf("phase %d: got %d want %d", first, got, expected) }
	}
	cursor.Install(ids.TxRef{}, nil, []ids.OID{0, 0, 1664})
	got := make([]ids.OID, 6)
	for i := range got { got[i] = cursor.Next() }
	if !slices.Equal(got, []ids.OID{0, 0, 1664, 0, 0, 1664}) &&
		!slices.Equal(got, []ids.OID{0, 1664, 0, 0, 1664, 0}) &&
		!slices.Equal(got, []ids.OID{1664, 0, 0, 1664, 0, 0}) {
		t.Fatal("duplicate multiplicity/order lost", got)
	}
}

func TestTempTablespacesOwnership(t *testing.T) {
	domain := &ids.Domain{}
	s := &session.Session{State: session.Live, SessionToken: domain.Session(1), Authorization: &session.AuthorizationState{}}
	owner := &session.TransactionOwner{Session: s}
	s.Transactions = owner
	owner.Ref = domain.Tx(s.SessionToken, 1, 1, owner, 1)
	store := &session.SettingsStore{Session: s, Values: map[string]string{}}
	store.InitializeShared(settings.NewRegistry(nil, false).SessionDefaults())
	s.Settings = store
	op := &session.Operation{Session: s}
	op.Self = op
	assign := func(value string, spaces []ids.OID, action session.SettingAction) *session.TempTablespaceAssignment {
		store.Touch("temp_tablespaces", action)
		store.Values["temp_tablespaces"] = value
		store.Typed["temp_tablespaces"] = settings.Value{Text: value}
		a := session.NewTempTablespaceAssignment(owner.Ref, spaces)
		store.AcceptTempTablespacesLocked(a)
		op.RefreshInvocationSettingsLocked(store)
		return a
	}
	first := assign("pg_default, pg_global", []ids.OID{0, 1664}, session.SettingSet)
	retained, ok := op.AcceptedTempTablespaces(owner.Ref)
	if !ok || len(retained) != 2 { t.Fatal("accepted list missing") }
	backing := &retained[0]
	mark := store.Savepoint()
	chosen := owner.TempTablespaces.Next()
	store.RestoreSavepointLocked(mark.Clone(), authorization.RoleState{})
	if !owner.TempTablespaces.Current(owner.Ref, first) || owner.TempTablespaces.Next() == chosen {
		t.Fatal("unchanged rollback rewound failed-consumption cursor")
	}
	second := assign("pg_global", []ids.OID{1664}, session.SettingLocal)
	if second == first || !owner.TempTablespaces.Current(owner.Ref, second) { t.Fatal("assignment identity not installed") }
	store.RestoreSavepointLocked(mark.Clone(), authorization.RoleState{})
	op.RefreshInvocationSettingsLocked(store)
	if store.TempTablespaces != first || owner.TempTablespaces.Current(owner.Ref, first) { t.Fatal("changed restoration reused stale cursor") }
	restored, ok := op.AcceptedTempTablespaces(owner.Ref)
	if !ok || &restored[0] != backing || !slices.Equal(retained, []ids.OID{0, 1664}) { t.Fatal("restoration lost immutable assignment backing") }
	// Same-string assignment still has a distinct check-hook identity and
	// installs a fresh selector; this is not raw-text cache invalidation.
	reassigned := assign("pg_default, pg_global", []ids.OID{0, 1664}, session.SettingSet)
	if reassigned == first || !owner.TempTablespaces.Current(owner.Ref, reassigned) { t.Fatal("same-value assignment was ignored") }
	op.Restricted = &session.RestrictedInvocation{}
	image := op.EffectiveSettings()
	if image == store || image.TempTablespaces == store.TempTablespaces { t.Fatal("detached owner reused assignment identity") }
	op.RefreshInvocationSettingsLocked(image)
	if owner.TempTablespaces.Current(owner.Ref, op.TempTablespaceAssignment) { t.Fatal("effective-store switch reused physical policy") }
	// The clone can share immutable OID backing, never the mutable cursor.
	cloned, ok := op.AcceptedTempTablespaces(owner.Ref)
	if !ok || len(cloned) != 2 || !slices.Equal(cloned, []ids.OID{0, 1664}) { t.Fatal("detached assignment lost accepted list") }
	op.Restricted = nil
	store.ResetAllLocked(settings.NewRegistry(nil, false))
	if store.TempTablespaces != nil || owner.TempTablespaces.Current(owner.Ref, reassigned) { t.Fatal("RESET ALL retained selector identity") }
	store.FinishTransaction(true)
	baseline := assign("pg_default", []ids.OID{0}, session.SettingSet)
	store.FinishTransaction(true)
	level := store.BeginSettingsSaveLocked()
	assign("pg_global", []ids.OID{1664}, session.SettingSave)
	store.EndSettingsSaveLocked(level, true)
	if store.TempTablespaces != baseline || owner.TempTablespaces.Current(owner.Ref, baseline) {
		t.Fatal("routine SAVE exit lost assignment/restoration invalidation")
	}
	assign("pg_global", []ids.OID{1664}, session.SettingLocal)
	store.FinishTransaction(true)
	if store.TempTablespaces != baseline || owner.TempTablespaces.Current(owner.Ref, baseline) {
		t.Fatal("LOCAL commit lost session assignment identity")
	}
}

func TestTempTablespacesLifecycle(t *testing.T) {
	domain := &ids.Domain{}
	owner := &session.TransactionOwner{}
	token := domain.Session(1)
	old := domain.Tx(token, 1, 1, owner, 1)
	next := domain.Tx(token, 2, 1, owner, 2)
	a := session.NewTempTablespaceAssignment(old, []ids.OID{1664})
	op := &session.Operation{TempTablespaceAssignment: a}
	owner.Ref = old
	owner.TempTablespaces.Install(old, a, []ids.OID{1664})
	if !owner.TempTablespaces.Current(old, a) || owner.TempTablespaces.Current(next, a) { t.Fatal("transaction identity ignored") }
	if _, ok := op.AcceptedTempTablespaces(next); ok { t.Fatal("next transaction reused accepted authority") }
	owner.Clear()
	if owner.TempTablespaces.Current(old, a) { t.Fatal("Clear retained cursor") }
	owner.InstallChain(next, session.TxOptions{}, time.Time{}, nil)
	owner.TempTablespaces.Install(next, a, []ids.OID{0})
	owner.Abort()
	if owner.TempTablespaces.Current(next, a) { t.Fatal("Abort retained cursor") }
	foreign := domain.Tx(token, 3, 2, owner, 3)
	if _, ok := op.AcceptedTempTablespaces(foreign); ok { t.Fatal("replacement epoch reused accepted authority") }
}

func TestTempTablespacesInvocationImage(t *testing.T) {
	for _, value := range []string{"", "pg_global"} {
		t.Run(value, func(t *testing.T) {
			store := &session.SettingsStore{Defaults: map[string]string{"temp_tablespaces": value}}
			s := &session.Session{Settings: store}
			op := &session.Operation{Session: s, Restricted: &session.RestrictedInvocation{}}
			before := store.Savepoint()
			image := op.EffectiveSettings()
			if image == store || !image.Invocation || image.Value("temp_tablespaces") != value {
				t.Fatal("detached invocation lost effective text or owner")
			}
			op.RefreshInvocationSettingsLocked(image)
			if _, accepted := op.AcceptedTempTablespaces(ids.TxRef{}); accepted {
				t.Fatal("unassigned invocation invented accepted check-hook data")
			}
			if value == "" {
				if image.TempTablespaces != nil || !reflect.DeepEqual(image.Savepoint(), before) {
					t.Fatal("untouched empty setting changed the complete journal image")
				}
			} else {
				if image.TempTablespaces == nil {
					t.Fatal("retained nonempty text lost effective-owner identity")
				}
				mark := op.CaptureInvocation()
				mark.Restore()
				restored := op.EffectiveSettings()
				if restored == image || restored.TempTablespaces == image.TempTablespaces {
					t.Fatal("retained invocation checkpoint reused owner identity")
				}
				if _, accepted := op.AcceptedTempTablespaces(ids.TxRef{}); accepted {
					t.Fatal("restored retained text invented accepted check-hook data")
				}
			}
			if !reflect.DeepEqual(store.Savepoint(), before) {
				t.Fatal("detaching mutated the source journal")
			}
		})
	}
}
