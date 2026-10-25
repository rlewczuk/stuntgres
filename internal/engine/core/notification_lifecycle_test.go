package core_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

func notificationAwaitWait(t *testing.T, s *session.Session, ctx context.Context) {
	t.Helper()
	for ctx.Err() == nil {
		core.EngineOf(s).Mu.Lock()
		waiting := core.PayloadNotifications(core.PayloadOf(s)).Wake != nil
		core.EngineOf(s).Mu.Unlock()
		if waiting {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("receiver did not register its wait", ctx.Err())
}

func TestNotificationSessionLifecycle(t *testing.T) {
	for _, reason := range []session.EndReason{session.CleanClose, session.InitFailure, session.InvalidConnection, session.Terminated, session.Abandoned} {
		t.Run(map[session.EndReason]string{session.CleanClose: "close", session.InitFailure: "init", session.InvalidConnection: "invalid", session.Terminated: "terminated", session.Abandoned: "abandoned"}[reason], func(t *testing.T) {
			e := engine.New()
			receiver, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			notificationUnitListen(t, receiver)
			notificationUnitListen(t, peer)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			op, err := receiver.AcquireActivity(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Keep a separate genuine lease to prove the receiver wakes before the
			// resource runner can drain leases and invoke EndSession.
			blocker, err := receiver.Acquire(ctx)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			defer blocker.Done()
			done := make(chan error, 1)
			go func() { _, err := core.EngineOf(receiver).WaitForNotification(op); op.Done(); done <- err }()
			notificationAwaitWait(t, receiver, ctx)
			receiver.RequestEnd(reason)
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("admitted wait terminal error", err)
				}
			case <-ctx.Done():
				t.Fatal("receiver held end lease", ctx.Err())
			}
			select {
			case <-receiver.Completed:
				t.Fatal("end bypassed live blocker lease")
			default:
			}
			blocker.Done()
			if err := receiver.WaitEnd(ctx); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			retired := core.PayloadNotifications(core.PayloadOf(receiver)).Ended && core.PayloadNotifications(core.PayloadOf(receiver)).State == nil && core.EngineNotifications(e)[receiver.SessionToken] == nil
			e.Mu.Unlock()
			if !retired {
				t.Fatal("physical recipient state retained")
			}
			notificationUnitNotify(t, peer, "peer-survives")
			if got := notificationUnitReceive(t, peer); got.Payload != "peer-survives" {
				t.Fatal(got)
			}
		})
	}
	t.Run("detach-and-initialization-unwind", func(t *testing.T) {
		e := engine.New()
		s := sessionStart(t, e, nil)
		notificationUnitListen(t, s)
		// Duplicate identity fails after normal resource attachment; initialized
		// resources must unwind without retiring the existing exact token owner.
		if duplicate, err := e.StartSession(context.Background(), session.SessionStart{ID: s.SessionToken.ID()}); duplicate != nil || err == nil {
			t.Fatal("duplicate initialization succeeded")
		}
		notificationUnitNotify(t, s, "still-owned")
		if got := notificationUnitReceive(t, s); got.Payload != "still-owned" {
			t.Fatal(got)
		}
		s.Mu.Lock()
		handle := s.Resources[session.Notifications].Handle
		s.Mu.Unlock()
		if err := s.Detach(handle, session.CleanClose); err != nil {
			t.Fatal(err)
		}
		if err := s.Detach(handle, session.CleanClose); err != nil {
			t.Fatal("repeat detach", err)
		}
		e.Mu.Lock()
		retired := core.PayloadNotifications(core.PayloadOf(s)).Ended && core.PayloadNotifications(core.PayloadOf(s)).State == nil && core.EngineNotifications(e)[s.SessionToken] == nil
		e.Mu.Unlock()
		if !retired {
			t.Fatal("detach retained queue/registration")
		}
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Fatal("end repeated detached resource", err)
		}
	})
}

func TestNotificationReceiveOwnership(t *testing.T) {
	e := engine.New()
	receiver, sender := sessionStart(t, e, nil), sessionStart(t, e, nil)
	notificationUnitListen(t, receiver)
	notificationUnitNotify(t, sender, "retained")
	ctx, cancel := context.WithCancel(context.Background())
	op, err := receiver.AcquireActivity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original := core.PayloadNotifications(core.PayloadOf(receiver)).State
	cancel()
	got, err := core.EngineOf(receiver).WaitForNotification(op)
	op.Done()
	if !errors.Is(err, context.Canceled) || got != (session.Notification{}) || core.PayloadNotifications(core.PayloadOf(receiver)).State != original {
		t.Fatal("cancellation consumed ready data", got, err)
	}
	if got := notificationUnitReceive(t, receiver); got.Payload != "retained" {
		t.Fatal(got)
	}

	// The actual registered waiter must permit an independent physical owner to
	// commit, without transaction admission or a lifecycle lock held by receive.
	wait, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	activity, err := receiver.AcquireActivity(wait)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		event session.Notification
		err   error
	}
	out := make(chan outcome, 2)
	go func() {
		event, err := core.EngineOf(receiver).WaitForNotification(activity)
		activity.Done()
		out <- outcome{event, err}
	}()
	notificationAwaitWait(t, receiver, wait)
	notificationUnitNotify(t, sender, "peer-progress")
	select {
	case result := <-out:
		if result.err != nil || result.event.Payload != "peer-progress" {
			t.Fatal(result)
		}
	case <-wait.Done():
		t.Fatal("peer commit did not wake receiver")
	}

	notificationUnitNotify(t, sender, "one")
	notificationUnitNotify(t, sender, "two")
	for range 2 {
		activity, err := receiver.AcquireActivity(wait)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			event, err := core.EngineOf(receiver).WaitForNotification(activity)
			activity.Done()
			out <- outcome{event, err}
		}()
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case result := <-out:
			if result.err != nil || seen[result.event.Payload] {
				t.Fatal("double consume", result)
			}
			seen[result.event.Payload] = true
		case <-wait.Done():
			t.Fatal("consumer did not finish")
		}
	}
	if !seen["one"] || !seen["two"] {
		t.Fatal("lost queue batch", seen)
	}
	e.Mu.Lock()
	queue := core.PayloadNotifications(core.PayloadOf(receiver)).State.Ready
	e.Mu.Unlock()
	if queue.Front != nil || queue.Back != nil || queue.Offset != 0 {
		t.Fatal("consumed links retained a batch")
	}
}

func TestNotificationBatchOwnership(t *testing.T) {
	e := engine.New()
	a, b, sender := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	notificationUnitListen(t, a)
	notificationUnitListen(t, b)
	ref := transactionBegin(t, sender)
	sender.Transactions.NotificationEvents = []session.Notification{
		{PID: uint64(sender.SessionToken.ID()), Channel: "unit", Payload: "first"},
		{PID: uint64(sender.SessionToken.ID()), Channel: "unit", Payload: "second"},
	}
	transactionEnd(t, sender, ref, true)
	e.Mu.Lock()
	left, right := core.PayloadNotifications(core.PayloadOf(a)).State.Ready.Back, core.PayloadNotifications(core.PayloadOf(b)).State.Ready.Back
	shared := left != nil && right != nil && left != right && &left.Batch[0] == &right.Batch[0]
	e.Mu.Unlock()
	if !shared {
		t.Fatal("publication failed shared-payload/private-link ownership")
	}
	if got := notificationUnitReceive(t, a); got.Payload != "first" {
		t.Fatal(got)
	}
	a.RequestEnd(session.CleanClose)
	if err := a.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	sender.RequestEnd(session.CleanClose)
	if err := sender.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"first", "second"} {
		if got := notificationUnitReceive(t, b); got.Payload != want {
			t.Fatal("peer retirement retracted immutable batch", got)
		}
	}
}
