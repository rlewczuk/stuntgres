package enginetests_test

import (
	"context"
	"maps"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func TestOverexplainSessionActivation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	physical := s.Settings
	defaults, typed := maps.Clone(physical.Defaults), maps.Clone(physical.DefaultTyped)
	mark := physical.Savepoint()
	op.Restricted = &session.RestrictedInvocation{Owner: s.Authorization.State.CurrentUser}
	parent := op.EffectiveSettings()
	checkpoint := op.CaptureInvocation()
	parentFrame := op.Restricted
	op.Restricted = &session.RestrictedInvocation{Owner: s.Authorization.State.CurrentUser, Parent: parentFrame}
	child := op.EffectiveSettings()
	if physical.OverexplainLoaded() || parent.OverexplainLoaded() || child.OverexplainLoaded() || parent == physical || child == parent {
		t.Fatal("not independent unloaded invocation images")
	}
	if err := child.LoadOverexplain(op.Context()); err != nil {
		t.Fatal(err)
	}
	for _, image := range []*session.SettingsStore{physical, parent, child} {
		if !image.OverexplainLoaded() {
			t.Fatal("activation did not reach original owner")
		}
	}
	op.Restricted = parentFrame
	checkpoint.PrepareRestore()
	checkpoint.Restore()
	restored := op.EffectiveSettings()
	if restored == parent || !restored.OverexplainLoaded() {
		t.Fatal("pre-load exception image froze recognition")
	}
	physical.Mu.Lock()
	s.Mu.Lock()
	physical.RestoreSavepointLocked(mark, s.Authorization.State)
	s.Mu.Unlock()
	physical.Mu.Unlock()
	physical.FinishTransaction(false)
	if !physical.OverexplainLoaded() || peer.Settings.OverexplainLoaded() {
		t.Fatal("rollback unloaded activation or leaked to peer")
	}
	if !reflect.DeepEqual(defaults, physical.Defaults) || !reflect.DeepEqual(typed, physical.DefaultTyped) || !reflect.DeepEqual(mark, physical.Savepoint()) {
		t.Fatal("module changed registry defaults or setting journal")
	}
	// Concurrent immutable-image reads exercise the actual atomic physical bit,
	// not copied booleans. End starts only after those readers/operations drain.
	var readers sync.WaitGroup
	for _, image := range []*session.SettingsStore{physical, parent, child, restored} {
		readers.Add(1)
		go func(image *session.SettingsStore) {
			defer readers.Done()
			for range 100 {
				if !image.OverexplainLoaded() {
					t.Error("lost shared recognition")
				}
			}
		}(image)
	}
	readers.Wait()
	op.Restricted = nil
	op.Done()
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, image := range []*session.SettingsStore{physical, parent, child, restored} {
		if image.OverexplainLoaded() {
			t.Fatal("physical end retained activation")
		}
		if err := image.LoadOverexplain(context.Background()); err == nil {
			t.Fatal("ended owner reactivated through retained image")
		}
	}
	if !physical.Ended || physical.Defaults != nil || physical.Typed != nil {
		t.Fatal("terminal resource cleanup retained physical state")
	}
	fresh := enginetest.StartSession(t, e, nil)
	if fresh.Settings.OverexplainLoaded() {
		t.Fatal("new physical owner imported recognition")
	}
}
