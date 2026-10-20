package engine

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func notificationAwaitWait(t *testing.T, s *Session, ctx context.Context) {
	t.Helper()
	for ctx.Err() == nil {
		engineOf(s).mu.Lock()
		waiting := payloadOf(s).notifications.wake != nil
		engineOf(s).mu.Unlock()
		if waiting {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("receiver did not register its wait", ctx.Err())
}

func TestNotificationSessionLifecycle(t *testing.T) {
	for _, reason := range []EndReason{CleanClose, InitFailure, InvalidConnection, Terminated, Abandoned} {
		t.Run(map[EndReason]string{CleanClose: "close", InitFailure: "init", InvalidConnection: "invalid", Terminated: "terminated", Abandoned: "abandoned"}[reason], func(t *testing.T) {
			e := New()
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
			go func() { _, err := engineOf(receiver).WaitForNotification(op); op.Done(); done <- err }()
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
			e.mu.Lock()
			retired := payloadOf(receiver).notifications.ended && payloadOf(receiver).notifications.state == nil && e.notifications[receiver.SessionToken] == nil
			e.mu.Unlock()
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
		e := New()
		s := sessionStart(t, e, nil)
		notificationUnitListen(t, s)
		// Duplicate identity fails after normal resource attachment; initialized
		// resources must unwind without retiring the existing exact token owner.
		if duplicate, err := e.StartSession(context.Background(), SessionStart{ID: s.SessionToken.ID()}); duplicate != nil || err == nil {
			t.Fatal("duplicate initialization succeeded")
		}
		notificationUnitNotify(t, s, "still-owned")
		if got := notificationUnitReceive(t, s); got.Payload != "still-owned" {
			t.Fatal(got)
		}
		s.Mu.Lock()
		handle := s.Resources[Notifications].Handle
		s.Mu.Unlock()
		if err := s.Detach(handle, CleanClose); err != nil {
			t.Fatal(err)
		}
		if err := s.Detach(handle, CleanClose); err != nil {
			t.Fatal("repeat detach", err)
		}
		e.mu.Lock()
		retired := payloadOf(s).notifications.ended && payloadOf(s).notifications.state == nil && e.notifications[s.SessionToken] == nil
		e.mu.Unlock()
		if !retired {
			t.Fatal("detach retained queue/registration")
		}
		s.RequestEnd(CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Fatal("end repeated detached resource", err)
		}
	})
}

func TestNotificationReceiveOwnership(t *testing.T) {
	e := New()
	receiver, sender := sessionStart(t, e, nil), sessionStart(t, e, nil)
	notificationUnitListen(t, receiver)
	notificationUnitNotify(t, sender, "retained")
	ctx, cancel := context.WithCancel(context.Background())
	op, err := receiver.AcquireActivity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original := payloadOf(receiver).notifications.state
	cancel()
	got, err := engineOf(receiver).WaitForNotification(op)
	op.Done()
	if !errors.Is(err, context.Canceled) || got != (Notification{}) || payloadOf(receiver).notifications.state != original {
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
		event Notification
		err   error
	}
	out := make(chan outcome, 2)
	go func() {
		event, err := engineOf(receiver).WaitForNotification(activity)
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
			event, err := engineOf(receiver).WaitForNotification(activity)
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
	e.mu.Lock()
	queue := payloadOf(receiver).notifications.state.ready
	e.mu.Unlock()
	if queue.front != nil || queue.back != nil || queue.offset != 0 {
		t.Fatal("consumed links retained a batch")
	}
}

func TestNotificationBatchOwnership(t *testing.T) {
	e := New()
	a, b, sender := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	notificationUnitListen(t, a)
	notificationUnitListen(t, b)
	ref := transactionBegin(t, sender)
	sender.Transactions.NotificationEvents = []Notification{
		{PID: uint64(sender.SessionToken.ID()), Channel: "unit", Payload: "first"},
		{PID: uint64(sender.SessionToken.ID()), Channel: "unit", Payload: "second"},
	}
	transactionEnd(t, sender, ref, true)
	e.mu.Lock()
	left, right := payloadOf(a).notifications.state.ready.back, payloadOf(b).notifications.state.ready.back
	shared := left != nil && right != nil && left != right && &left.batch[0] == &right.batch[0]
	e.mu.Unlock()
	if !shared {
		t.Fatal("publication failed shared-payload/private-link ownership")
	}
	if got := notificationUnitReceive(t, a); got.Payload != "first" {
		t.Fatal(got)
	}
	a.RequestEnd(CleanClose)
	if err := a.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	sender.RequestEnd(CleanClose)
	if err := sender.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"first", "second"} {
		if got := notificationUnitReceive(t, b); got.Payload != want {
			t.Fatal("peer retirement retracted immutable batch", got)
		}
	}
}
